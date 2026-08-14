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
	Kind     Kind   `json:"kind"`
	ID       int64  `json:"id"`
	ParentID int64  `json:"parent_id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	MD5      string `json:"md5,omitempty"`
}

// Manifest authorizes exactly one isolated live-test campaign.
type Manifest struct {
	Version      int      `json:"version"`
	Mode         Mode     `json:"mode"`
	Session      string   `json:"session"`
	UID          int64    `json:"uid"`
	AnchorID     int64    `json:"anchor_id"`
	WorkRootID   int64    `json:"work_root_id"`
	AnchorRemote string   `json:"anchor_remote"`
	Limits       Limits   `json:"limits"`
	Usage        Usage    `json:"usage"`
	Sentinels    []Object `json:"sentinels"`
	Objects      []Object `json:"objects"`
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
	if m.Usage.Files < len(m.Sentinels) {
		return errors.New("文件配额计数未包含两个 sentinel")
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
	m.Objects = append(m.Objects, object)
	return nil
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
