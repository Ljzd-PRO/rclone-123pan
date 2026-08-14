// Package livetest provides the fail-closed manifest used by authorized
// 123Pan live-account tests. It deliberately stores no credentials, tokens,
// upload keys, cookies, or signed URLs.
package livetest

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

const (
	ManifestVersion = 1
	Mebi            = int64(1024 * 1024)

	HardMaxFiles       = 100
	HardMaxDirectories = 50
	HardMaxSingleFile  = 160*Mebi + 1
	HardMaxPayload     = 512 * Mebi
)

var sessionNameRE = regexp.MustCompile(`^rclone-test-[a-z0-9]{12,64}$`)

// Mode selects the scope of live testing authorized by the manifest.
type Mode string

const (
	ModeIsolated          Mode = "isolated"
	ModeDedicatedContract Mode = "dedicated-contract"
)

// Kind is the server object kind recorded in the recovery ledger.
type Kind string

const (
	KindFile      Kind = "file"
	KindDirectory Kind = "directory"
)

// CleanupState is a monotonic recovery state for objects created by a live
// campaign. Empty is accepted as the legacy spelling of active.
type CleanupState string

const (
	CleanupActive           CleanupState = "active"
	CleanupTrashed          CleanupState = "trashed"
	CleanupMissingConfirmed CleanupState = "missing_confirmed"
)

// Limits are immutable upper bounds for one live-test campaign.
type Limits struct {
	MaxFiles       int   `json:"max_files"`
	MaxDirectories int   `json:"max_directories"`
	MaxSingleFile  int64 `json:"max_single_file"`
	MaxPayload     int64 `json:"max_payload"`
}

// DefaultLimits returns the limits approved for the current campaign.
func DefaultLimits() Limits {
	return Limits{
		MaxFiles:       HardMaxFiles,
		MaxDirectories: HardMaxDirectories,
		MaxSingleFile:  HardMaxSingleFile,
		MaxPayload:     HardMaxPayload,
	}
}

// Usage counts allocations cumulatively. Deleting an object never refunds a
// quota because an ambiguous upload may still consume provider-side state.
type Usage struct {
	Files       int   `json:"files"`
	Directories int   `json:"directories"`
	Payload     int64 `json:"payload"`
}

// Object is a non-secret identity record used for verification and recovery.
type Object struct {
	Kind     Kind         `json:"kind"`
	ID       int64        `json:"id"`
	ParentID int64        `json:"parent_id"`
	Name     string       `json:"name"`
	Size     int64        `json:"size"`
	MD5      string       `json:"md5,omitempty"`
	Cleanup  CleanupState `json:"cleanup,omitempty"`
}

// UploadAllocation records a known provider-side ID which never became a
// verified visible object. It is recovery evidence only and is deliberately
// ineligible for automatic deletion.
type UploadAllocation struct {
	ID       int64  `json:"id"`
	ParentID int64  `json:"parent_id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	MD5      string `json:"md5"`
}

type rcloneListEntry struct {
	Path   string            `json:"Path"`
	Name   string            `json:"Name"`
	Size   int64             `json:"Size"`
	IsDir  bool              `json:"IsDir"`
	Hashes map[string]string `json:"Hashes"`
	ID     string            `json:"ID"`
}

// Manifest authorizes exactly one isolated live-test campaign.
type Manifest struct {
	Version           int                `json:"version"`
	Mode              Mode               `json:"mode"`
	Session           string             `json:"session"`
	UID               int64              `json:"uid"`
	AnchorID          int64              `json:"anchor_id"`
	WorkRootID        int64              `json:"work_root_id"`
	AnchorRemote      string             `json:"anchor_remote"`
	Limits            Limits             `json:"limits"`
	Usage             Usage              `json:"usage"`
	Sentinels         []Object           `json:"sentinels"`
	Objects           []Object           `json:"objects"`
	UnresolvedUploads []UploadAllocation `json:"unresolved_uploads,omitempty"`
}

// NewManifest creates an empty manifest. The caller must add exactly two
// verified sentinel files before the manifest is accepted for live tests.
func NewManifest(mode Mode, session string, uid, anchorID, workRootID int64, anchorRemote string) *Manifest {
	return &Manifest{
		Version:      ManifestVersion,
		Mode:         mode,
		Session:      session,
		UID:          uid,
		AnchorID:     anchorID,
		WorkRootID:   workRootID,
		AnchorRemote: anchorRemote,
		Limits:       DefaultLimits(),
	}
}

func normalizeMD5(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != md5.Size*2 {
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return value
}

func validateObject(object Object, requireFileHash bool) error {
	if object.ID <= 0 || object.ParentID <= 0 {
		return fmt.Errorf("对象 %q 的 ID 或 parent ID 无效", object.Name)
	}
	if object.Name == "" || object.Name == "." || object.Name == ".." || strings.ContainsAny(object.Name, "/\x00") {
		return fmt.Errorf("对象 ID %d 的名称不安全", object.ID)
	}
	if object.Size < 0 {
		return fmt.Errorf("对象 ID %d 的大小为负数", object.ID)
	}
	switch object.Kind {
	case KindFile:
		if requireFileHash && normalizeMD5(object.MD5) == "" {
			return fmt.Errorf("文件 ID %d 缺少有效 MD5", object.ID)
		}
	case KindDirectory:
		if object.Size != 0 || object.MD5 != "" {
			return fmt.Errorf("目录 ID %d 含有文件字段", object.ID)
		}
	default:
		return fmt.Errorf("对象 ID %d 的类型 %q 无效", object.ID, object.Kind)
	}
	switch object.Cleanup {
	case "", CleanupActive, CleanupTrashed, CleanupMissingConfirmed:
	default:
		return fmt.Errorf("对象 ID %d 的清理状态 %q 无效", object.ID, object.Cleanup)
	}
	return nil
}

func validateUploadAllocation(upload UploadAllocation) error {
	if upload.ID <= 0 || upload.ParentID <= 0 {
		return fmt.Errorf("未解析上传 %q 的 ID 或 parent ID 无效", upload.Name)
	}
	if upload.Name == "" || upload.Name == "." || upload.Name == ".." || strings.ContainsAny(upload.Name, "/\x00") {
		return fmt.Errorf("未解析上传 ID %d 的名称不安全", upload.ID)
	}
	if upload.Size < 0 || normalizeMD5(upload.MD5) == "" {
		return fmt.Errorf("未解析上传 ID %d 的大小或 MD5 无效", upload.ID)
	}
	return nil
}

// Validate checks identity, quotas, sentinel invariants, and ledger uniqueness.
func (m *Manifest) Validate() error {
	if m == nil {
		return errors.New("live manifest 为空")
	}
	if m.Version != ManifestVersion {
		return fmt.Errorf("live manifest 版本 %d 不受支持", m.Version)
	}
	if m.Mode != ModeIsolated && m.Mode != ModeDedicatedContract {
		return fmt.Errorf("live manifest 模式 %q 无效", m.Mode)
	}
	if !sessionNameRE.MatchString(m.Session) {
		return fmt.Errorf("session 名称 %q 不符合隔离命名规则", m.Session)
	}
	if m.UID <= 0 || m.AnchorID <= 0 || m.WorkRootID <= 0 || m.AnchorID == m.WorkRootID {
		return errors.New("UID、anchor ID 或 work root ID 无效")
	}
	if strings.TrimSpace(m.AnchorRemote) == "" {
		return errors.New("anchor remote 不能为空")
	}
	if m.Limits.MaxFiles <= 0 || m.Limits.MaxFiles > HardMaxFiles ||
		m.Limits.MaxDirectories <= 0 || m.Limits.MaxDirectories > HardMaxDirectories ||
		m.Limits.MaxSingleFile <= 0 || m.Limits.MaxSingleFile > HardMaxSingleFile ||
		m.Limits.MaxPayload <= 0 || m.Limits.MaxPayload > HardMaxPayload {
		return errors.New("live manifest 尝试放宽硬配额")
	}
	if m.Usage.Files < 0 || m.Usage.Files > m.Limits.MaxFiles ||
		m.Usage.Directories < 0 || m.Usage.Directories > m.Limits.MaxDirectories ||
		m.Usage.Payload < 0 || m.Usage.Payload > m.Limits.MaxPayload {
		return errors.New("live manifest 已超出配额")
	}
	if len(m.Sentinels) != 2 {
		return fmt.Errorf("需要恰好两个 sentinel，实际为 %d", len(m.Sentinels))
	}
	seen := make(map[int64]string, len(m.Sentinels)+len(m.Objects))
	for _, sentinel := range m.Sentinels {
		if sentinel.Kind != KindFile || sentinel.ParentID != m.AnchorID {
			return fmt.Errorf("sentinel ID %d 不在 anchor 中或不是文件", sentinel.ID)
		}
		if sentinel.Cleanup != "" && sentinel.Cleanup != CleanupActive {
			return fmt.Errorf("sentinel ID %d 不能标记为已清理", sentinel.ID)
		}
		if err := validateObject(sentinel, true); err != nil {
			return err
		}
		if previous := seen[sentinel.ID]; previous != "" {
			return fmt.Errorf("对象 ID %d 在 %s 和 sentinel 中重复", sentinel.ID, previous)
		}
		seen[sentinel.ID] = "sentinel"
	}
	for _, object := range m.Objects {
		if err := validateObject(object, object.Kind == KindFile); err != nil {
			return err
		}
		if previous := seen[object.ID]; previous != "" {
			return fmt.Errorf("对象 ID %d 在 %s 和 ledger 中重复", object.ID, previous)
		}
		seen[object.ID] = "ledger"
	}
	for _, upload := range m.UnresolvedUploads {
		if err := validateUploadAllocation(upload); err != nil {
			return err
		}
		if previous := seen[upload.ID]; previous != "" {
			return fmt.Errorf("对象 ID %d 在 %s 和未解析上传中重复", upload.ID, previous)
		}
		seen[upload.ID] = "unresolved upload"
	}
	recordedFiles := len(m.Sentinels) + len(m.UnresolvedUploads)
	recordedDirectories := 0
	for _, object := range m.Objects {
		if object.Kind == KindDirectory {
			recordedDirectories++
		} else {
			recordedFiles++
		}
	}
	if m.Usage.Files < recordedFiles || m.Usage.Directories < recordedDirectories {
		return errors.New("恢复记录中的文件或目录数量超过已预留配额")
	}
	return nil
}

// ReserveFile consumes one file allocation and its payload before an upload
// request is made. The reservation is never refunded.
func (m *Manifest) ReserveFile(size int64) error {
	if size < 0 || size > m.Limits.MaxSingleFile {
		return fmt.Errorf("文件大小 %d 超出 live 测试上限", size)
	}
	if m.Usage.Files >= m.Limits.MaxFiles || m.Usage.Payload > m.Limits.MaxPayload-size {
		return errors.New("live 文件数量或 payload 配额不足")
	}
	m.Usage.Files++
	m.Usage.Payload += size
	return nil
}

// ReserveDirectory consumes one directory allocation. It is never refunded.
func (m *Manifest) ReserveDirectory() error {
	if m.Usage.Directories >= m.Limits.MaxDirectories {
		return errors.New("live 目录配额不足")
	}
	m.Usage.Directories++
	return nil
}

// RecordObject appends an exact identity to the recovery ledger.
func (m *Manifest) RecordObject(object Object) error {
	if err := validateObject(object, object.Kind == KindFile); err != nil {
		return err
	}
	for _, sentinel := range m.Sentinels {
		if sentinel.ID == object.ID {
			return fmt.Errorf("对象 ID %d 已被 sentinel 使用", object.ID)
		}
	}
	for _, existing := range m.Objects {
		if existing.ID == object.ID {
			return fmt.Errorf("对象 ID %d 已在 ledger 中", object.ID)
		}
	}
	for _, upload := range m.UnresolvedUploads {
		if upload.ID == object.ID {
			return fmt.Errorf("对象 ID %d 已在未解析上传记录中", object.ID)
		}
	}
	if object.Cleanup == "" {
		object.Cleanup = CleanupActive
	}
	m.Objects = append(m.Objects, object)
	return nil
}

// RecordUnresolvedUpload appends a known allocation that was never proven to
// be a visible object. No mutation helper accepts these entries as targets.
func (m *Manifest) RecordUnresolvedUpload(upload UploadAllocation) error {
	if err := validateUploadAllocation(upload); err != nil {
		return err
	}
	for _, sentinel := range m.Sentinels {
		if sentinel.ID == upload.ID {
			return fmt.Errorf("上传 ID %d 已被 sentinel 使用", upload.ID)
		}
	}
	for _, object := range m.Objects {
		if object.ID == upload.ID {
			return fmt.Errorf("上传 ID %d 已在对象 ledger 中", upload.ID)
		}
	}
	for _, existing := range m.UnresolvedUploads {
		if existing.ID == upload.ID {
			return fmt.Errorf("上传 ID %d 已在未解析上传记录中", upload.ID)
		}
	}
	m.UnresolvedUploads = append(m.UnresolvedUploads, upload)
	return nil
}

// ImportRcloneList imports only new, direct children from a complete lsjson
// array. A caller-provided random prefix and exact expected count prevent a
// broad account listing from being turned into deletion authority.
func (m *Manifest) ImportRcloneList(reader io.Reader, parentID int64, prefix string, expectedNew int) error {
	if parentID != m.WorkRootID || prefix == "" || strings.ContainsAny(prefix, "/\x00") || expectedNew < 0 {
		return errors.New("lsjson 导入范围无效")
	}
	decoder := json.NewDecoder(io.LimitReader(reader, 16*Mebi))
	var entries []rcloneListEntry
	if err := decoder.Decode(&entries); err != nil {
		return fmt.Errorf("解析 lsjson: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("lsjson 含有多余 JSON")
	}
	existing := make(map[int64]struct{}, len(m.Objects)+len(m.Sentinels)+len(m.UnresolvedUploads))
	knownObjects := make(map[int64]Object, len(m.Objects))
	for _, sentinel := range m.Sentinels {
		existing[sentinel.ID] = struct{}{}
	}
	for _, object := range m.Objects {
		existing[object.ID] = struct{}{}
		knownObjects[object.ID] = object
	}
	for _, upload := range m.UnresolvedUploads {
		existing[upload.ID] = struct{}{}
	}
	seenInput := make(map[int64]struct{}, len(entries))
	seenNames := make(map[string]int64, len(entries))
	newObjects := make([]Object, 0, expectedNew)
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name, prefix) {
			continue
		}
		if entry.Path != entry.Name || !validImportName(entry.Name) {
			return fmt.Errorf("lsjson 条目 %q 不是安全的直系子项", entry.Name)
		}
		id, err := strconv.ParseInt(entry.ID, 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("lsjson 条目 %q 的 ID 无效", entry.Name)
		}
		if _, duplicate := seenInput[id]; duplicate {
			return fmt.Errorf("lsjson 重复对象 ID %d", id)
		}
		seenInput[id] = struct{}{}
		if otherID, duplicate := seenNames[entry.Name]; duplicate && otherID != id {
			return fmt.Errorf("lsjson 名称 %q 在 ID %d 和 %d 之间歧义", entry.Name, otherID, id)
		}
		seenNames[entry.Name] = id
		if _, known := existing[id]; known {
			knownObject, isObject := knownObjects[id]
			kindMatches := isObject && ((entry.IsDir && knownObject.Kind == KindDirectory) || (!entry.IsDir && knownObject.Kind == KindFile))
			sizeMatches := entry.IsDir || knownObject.Size == entry.Size
			hashMatches := entry.IsDir || normalizeMD5(knownObject.MD5) == normalizeMD5(entry.Hashes["md5"])
			if !kindMatches || knownObject.ParentID != parentID || knownObject.Name != entry.Name || !sizeMatches || !hashMatches {
				return fmt.Errorf("已记录对象 ID %d 的 lsjson 身份发生变化", id)
			}
			continue
		}
		object := Object{ID: id, ParentID: parentID, Name: entry.Name, Size: entry.Size, MD5: entry.Hashes["md5"]}
		if entry.IsDir {
			object.Kind = KindDirectory
			object.Size = 0
			object.MD5 = ""
		} else {
			object.Kind = KindFile
		}
		if err := validateObject(object, !entry.IsDir); err != nil {
			return err
		}
		newObjects = append(newObjects, object)
	}
	if len(newObjects) != expectedNew {
		return fmt.Errorf("lsjson 新增前缀对象 %d 个，不是预期的 %d 个", len(newObjects), expectedNew)
	}
	for _, object := range newObjects {
		if err := m.RecordObject(object); err != nil {
			return err
		}
	}
	return nil
}

func validImportName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\x00")
}

// MarkCleanup advances one ledger object to a stronger cleanup proof. States
// never move backwards and sentinels are not eligible.
func (m *Manifest) MarkCleanup(id int64, state CleanupState) error {
	if state != CleanupTrashed && state != CleanupMissingConfirmed {
		return fmt.Errorf("目标清理状态 %q 无效", state)
	}
	for i := range m.Objects {
		object := &m.Objects[i]
		if object.ID != id {
			continue
		}
		current := object.Cleanup
		if current == "" {
			current = CleanupActive
		}
		rank := map[CleanupState]int{CleanupActive: 0, CleanupTrashed: 1, CleanupMissingConfirmed: 2}
		if rank[state] < rank[current] {
			return fmt.Errorf("对象 ID %d 的清理状态不能从 %q 回退到 %q", id, current, state)
		}
		object.Cleanup = state
		return nil
	}
	return fmt.Errorf("ledger 中不存在对象 ID %d", id)
}

// RelocateObject updates only the current parent/name identity of one active
// ledger object after an ID-verified rename or move. Cleaned objects are
// immutable because their last visible identity is recovery evidence.
func (m *Manifest) RelocateObject(id, parentID int64, name string) error {
	for i := range m.Objects {
		object := &m.Objects[i]
		if object.ID != id {
			continue
		}
		cleanup := object.Cleanup
		if cleanup == "" {
			cleanup = CleanupActive
		}
		if cleanup != CleanupActive {
			return fmt.Errorf("对象 ID %d 已进入清理流程，不能更新位置", id)
		}
		updated := *object
		updated.ParentID = parentID
		updated.Name = name
		if err := validateObject(updated, updated.Kind == KindFile); err != nil {
			return err
		}
		*object = updated
		return nil
	}
	return fmt.Errorf("ledger 中不存在对象 ID %d", id)
}

// LookupSentinel returns the currently visible object for an expected
// sentinel. Implementations must perform a fresh provider lookup.
type LookupSentinel func(context.Context, Object) (Object, error)

// VerifySentinels checks both sentinels byte-for-byte at the metadata level.
func (m *Manifest) VerifySentinels(ctx context.Context, lookup LookupSentinel) error {
	if err := m.Validate(); err != nil {
		return err
	}
	for _, expected := range m.Sentinels {
		actual, err := lookup(ctx, expected)
		if err != nil {
			return fmt.Errorf("查询 sentinel ID %d: %w", expected.ID, err)
		}
		actual.MD5 = normalizeMD5(actual.MD5)
		expected.MD5 = normalizeMD5(expected.MD5)
		if actual != expected {
			return fmt.Errorf("sentinel ID %d 已变化", expected.ID)
		}
	}
	return nil
}

// Load reads a strict, owner-only JSON manifest.
func Load(path string) (*Manifest, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("live manifest 不是普通文件")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("live manifest 权限必须为 0600，实际为 %04o", info.Mode().Perm())
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("解析 live manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("live manifest 含有多余 JSON")
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	return &manifest, nil
}

// Save atomically writes an owner-only manifest without following a broad or
// implicit path. Credentials must never be added to this structure.
func (m *Manifest) Save(path string) error {
	if err := m.Validate(); err != nil {
		return err
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".rclone-123-live-manifest-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryName)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(m); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return err
	}
	removeTemporary = false
	return nil
}
