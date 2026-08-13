// Package pan123 implements an experimental 123Pan personal-account backend.
package pan123

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ljzd/rclone-123pan/backend/pan123/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/config/configstruct"
	"github.com/rclone/rclone/fs/config/obscure"
	"github.com/rclone/rclone/fs/fshttp"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/lib/dircache"
	"github.com/rclone/rclone/lib/encoder"
)

const defaultEncoding = encoder.Display |
	encoder.EncodeWin |
	encoder.EncodeBackSlash |
	encoder.EncodeLeftSpace |
	encoder.EncodeRightSpace |
	encoder.EncodeRightPeriod |
	encoder.EncodeInvalidUtf8

func init() {
	fs.Register(&fs.RegInfo{
		Name:        "123pan",
		Prefix:      "pan123",
		Description: "123Pan personal account (experimental)",
		NewFs:       NewFs,
		CommandHelp: commandHelp,
		Options: []fs.Option{
			{Name: "user", Help: "Phone number or email used by the 123Pan personal account.", Required: true, Sensitive: true},
			{Name: "pass", Help: "123Pan account password.", Required: true, IsPassword: true, Sensitive: true},
			{Name: "access_token", Help: "Cached personal-account access token.", Sensitive: true, Hide: fs.OptionHideBoth},
			{Name: "root_folder_id", Help: "Numeric folder ID used as the account root.", Default: "0", Advanced: true, Sensitive: true},
			{Name: "platform", Help: "Platform request header. Protocol signatures always use web/3.", Default: "web", Advanced: true},
			{Name: "upload_concurrency", Help: "Maximum number of data parts uploaded concurrently for one file.", Default: 3, Advanced: true},
			{Name: "hash_memory_limit", Help: "Maximum source size cached in memory when an MD5 must be calculated.", Default: fs.SizeSuffix(10 * fs.Mebi), Advanced: true},
			{Name: "api_min_interval", Help: "Minimum interval between requests to the same control endpoint.", Default: fs.Duration(700 * time.Millisecond), Advanced: true},
			{Name: "verify_timeout", Help: "Maximum time to wait for a write postcondition.", Default: fs.Duration(60 * time.Second), Advanced: true},
			{Name: "encoding", Help: "The encoding for the backend.", Default: defaultEncoding, Advanced: true},
		},
	})
}

// Options defines persisted remote configuration.
type Options struct {
	User              string               `config:"user"`
	Pass              string               `config:"pass"`
	AccessToken       string               `config:"access_token"`
	RootFolderID      string               `config:"root_folder_id"`
	Platform          string               `config:"platform"`
	UploadConcurrency int                  `config:"upload_concurrency"`
	HashMemoryLimit   fs.SizeSuffix        `config:"hash_memory_limit"`
	APIMinInterval    fs.Duration          `config:"api_min_interval"`
	VerifyTimeout     fs.Duration          `config:"verify_timeout"`
	Enc               encoder.MultiEncoder `config:"encoding"`
}

// Fs represents a 123Pan personal-account remote.
type Fs struct {
	name           string
	root           string
	opt            Options
	client         *apiClient
	features       *fs.Features
	dirCache       *dircache.DirCache
	uid            int64
	downloadClient *http.Client
	locks          *keyedLocks
	stageName      func(string) (string, error)
}

// NewFs constructs and validates a personal-account remote.
func NewFs(ctx context.Context, name, root string, m configmap.Mapper) (fs.Fs, error) {
	var opt Options
	if err := configstruct.Set(m, &opt); err != nil {
		return nil, fmt.Errorf("parse 123Pan configuration: %w", err)
	}
	if opt.User == "" {
		return nil, errorsNewConfig("user is required")
	}
	if opt.Pass == "" {
		return nil, errorsNewConfig("pass is required")
	}
	if opt.UploadConcurrency < 1 || opt.UploadConcurrency > 10 {
		return nil, errorsNewConfig("upload_concurrency must be in 1..10")
	}
	if opt.HashMemoryLimit < 0 {
		return nil, errorsNewConfig("hash_memory_limit must not be negative")
	}
	if time.Duration(opt.APIMinInterval) < 0 {
		return nil, errorsNewConfig("api_min_interval must not be negative")
	}
	if time.Duration(opt.VerifyTimeout) <= 0 {
		return nil, errorsNewConfig("verify_timeout must be positive")
	}
	rootID, err := strconv.ParseInt(opt.RootFolderID, 10, 64)
	if err != nil || rootID < 0 {
		return nil, errorsNewConfig("root_folder_id must be a non-negative integer")
	}
	password, err := obscure.Reveal(opt.Pass)
	if err != nil {
		return nil, fmt.Errorf("reveal 123Pan password: %w", err)
	}
	c := newAPIClient(ctx, productionLoginRoot, productionAPIRoot, m, opt.User, password, opt.Platform, opt.AccessToken, time.Duration(opt.APIMinInterval))
	if opt.AccessToken == "" {
		if err := c.login(ctx, ""); err != nil {
			return nil, fmt.Errorf("authenticate 123Pan account: %w", err)
		}
	}
	var user api.UserInfoData
	if err := c.do(ctx, "GET", api.UserInfoPath, nil, &user); err != nil {
		return nil, fmt.Errorf("validate 123Pan account: %w", err)
	}
	if user.UID <= 0 {
		return nil, fmt.Errorf("validate 123Pan account: invalid UID %d", user.UID)
	}

	// Validate the configured ID independently from the command path. The
	// command path itself may legitimately not exist yet (for example
	// `rclone mkdir remote:new-directory`).
	if _, err := cListAll(ctx, c, opt, rootID); err != nil {
		return nil, fmt.Errorf("validate configured 123Pan root folder ID: %w", err)
	}

	f := &Fs{name: name, root: strings.Trim(root, "/"), opt: opt, client: c, uid: user.UID, downloadClient: fshttp.NewClient(ctx), locks: locksForUID(user.UID), stageName: randomStageName}
	f.dirCache = dircache.New(f.root, opt.RootFolderID, f)
	f.features = (&fs.Features{
		CaseInsensitive:         false,
		CanHaveEmptyDirectories: true,
		PartialUploads:          true,
		DuplicateFiles:          false,
	}).Fill(ctx, f)
	if err := f.resolveRoot(ctx); err != nil {
		return f, err
	}
	return f, nil
}

// cListAll validates the configured root before an Fs exists. It deliberately
// uses the same strict listing implementation as normal operations.
func cListAll(ctx context.Context, client *apiClient, opt Options, parentID int64) ([]api.File, error) {
	temporary := &Fs{client: client, opt: opt}
	return temporary.listAll(ctx, parentID)
}

// resolveRoot follows normal rclone NewFs semantics: an absent command root is
// allowed so Mkdir can create it, while a root naming an existing file returns
// fs.ErrorIsFile with the Fs adjusted to the parent directory.
func (f *Fs) resolveRoot(ctx context.Context) error {
	err := f.dirCache.FindRoot(ctx, false)
	if err == nil {
		resolvedID, rootErr := f.dirCache.RootID(ctx, false)
		if rootErr != nil {
			return fmt.Errorf("resolve 123Pan root ID: %w", rootErr)
		}
		rootID, parseErr := strconv.ParseInt(resolvedID, 10, 64)
		if parseErr != nil || rootID < 0 {
			return errorsNewProtocol("resolved root has an invalid ID")
		}
		if _, listErr := f.listAll(ctx, rootID); listErr != nil {
			return fmt.Errorf("validate 123Pan root directory: %w", listErr)
		}
		return nil
	}
	if !errors.Is(err, fs.ErrorDirNotFound) && !errors.Is(err, fs.ErrorIsFile) {
		return fmt.Errorf("resolve 123Pan root: %w", err)
	}

	parentRoot, leaf := dircache.SplitPath(f.root)
	temporary := *f
	temporary.root = parentRoot
	temporary.dirCache = dircache.New(parentRoot, f.opt.RootFolderID, &temporary)
	if parentErr := temporary.dirCache.FindRoot(ctx, false); parentErr != nil {
		if errors.Is(parentErr, fs.ErrorDirNotFound) {
			f.dirCache = dircache.New(f.root, f.opt.RootFolderID, f)
			return nil
		}
		return fmt.Errorf("resolve parent of missing 123Pan root: %w", parentErr)
	}
	if _, objectErr := temporary.NewObject(ctx, leaf); objectErr != nil {
		if errors.Is(objectErr, fs.ErrorObjectNotFound) {
			f.dirCache = dircache.New(f.root, f.opt.RootFolderID, f)
			return nil
		}
		return objectErr
	}
	f.root = temporary.root
	f.dirCache = temporary.dirCache
	f.features = (&fs.Features{
		CaseInsensitive:         false,
		CanHaveEmptyDirectories: true,
		PartialUploads:          true,
		DuplicateFiles:          false,
	}).Fill(ctx, f)
	return fs.ErrorIsFile
}

func errorsNewConfig(message string) error {
	return fmt.Errorf("invalid 123Pan configuration: %s", message)
}

// Name returns the configured remote name.
func (f *Fs) Name() string { return f.name }

// Root returns the configured path root.
func (f *Fs) Root() string { return f.root }

// String describes the remote without including credentials.
func (f *Fs) String() string { return "123Pan personal account root '" + f.root + "'" }

// Precision reports that server modification times cannot be set.
func (f *Fs) Precision() time.Duration { return fs.ModTimeNotSupported }

// Hashes reports the verified object checksum type.
func (f *Fs) Hashes() hash.Set { return hash.Set(hash.MD5) }

// Features returns optional rclone capabilities.
func (f *Fs) Features() *fs.Features { return f.features }

// Put creates a new object. Existing-object replacement is delegated to the
// recoverable Update state machine.
func (f *Fs) Put(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (fs.Object, error) {
	leaf, parent, err := f.dirCache.FindPath(ctx, src.Remote(), false)
	if err != nil {
		return nil, err
	}
	parentID, err := parseID(parent, true)
	if err != nil {
		return nil, err
	}
	f.ensureLocks()
	serverName := f.opt.Enc.FromStandardName(leaf)
	unlock := f.locks.lock(
		parentMutationLockKey(parentID),
		objectPathLockKey(parentID, serverName),
	)
	defer unlock()
	parentRemote, _ := dircache.SplitPath(src.Remote())
	if err := f.verifyDirectoryPathID(ctx, parentRemote, parentID); err != nil {
		return nil, err
	}
	existing, found, err := f.findChild(ctx, parentID, leaf)
	if err != nil {
		return nil, err
	}
	if found {
		if existing.IsDir() {
			return nil, fs.ErrorIsDir
		}
		o := newObject(f, src.Remote(), parentID, *existing)
		return o, o.updateLocked(ctx, in, src)
	}
	return f.uploadNew(ctx, in, src, parentID, leaf, src.Remote())
}

// PutStream accepts unknown-size inputs; prepareSource resolves the true size
// before any upload request is sent.
func (f *Fs) PutStream(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (fs.Object, error) {
	return f.Put(ctx, in, src, options...)
}

// Disconnect invalidates the server session and clears the cached token.
func (f *Fs) Disconnect(ctx context.Context) error {
	err := f.client.doNonIdempotent(ctx, http.MethodPost, api.LogoutPath, struct{}{}, nil)
	f.client.setToken("")
	f.client.config.Set("access_token", "")
	return err
}

var _ fs.Fs = (*Fs)(nil)
var _ fs.Disconnecter = (*Fs)(nil)
var _ fs.Abouter = (*Fs)(nil)
var _ fs.UserInfoer = (*Fs)(nil)
var _ fs.DirCacheFlusher = (*Fs)(nil)
var _ fs.PutStreamer = (*Fs)(nil)
var _ fs.Mover = (*Fs)(nil)
var _ fs.DirMover = (*Fs)(nil)
var _ fs.Commander = (*Fs)(nil)
var _ dircache.DirCacher = (*Fs)(nil)
