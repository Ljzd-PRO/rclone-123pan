// Package api contains the wire types used by the 123Pan personal-account API.
package api

import (
	"encoding/json"
	"time"
)

// Endpoint paths are relative to the production API roots.
const (
	SignInPath            = "/api/user/sign_in"
	LogoutPath            = "/user/logout"
	UserInfoPath          = "/user/info"
	FileListPath          = "/file/list/new"
	DownloadInfoPath      = "/file/download_info"
	UploadRequestPath     = "/file/upload_request"
	MovePath              = "/file/mod_pid"
	RenamePath            = "/file/rename"
	TrashPath             = "/file/trash"
	UploadCompletePath    = "/file/upload_complete"
	S3CompletePath        = "/file/s3_complete_multipart_upload"
	PresignedPartsPath    = "/file/s3_repare_upload_parts_batch"
	SingleObjectAuthPath  = "/file/s3_upload_object/auth"
	UploadCompleteV2Path  = "/file/upload_complete/v2"
	OfflineResolvePath    = "/v2/offline_download/task/resolve"
	OfflineSubmitPath     = "/v2/offline_download/task/submit"
	OfflineTaskListPath   = "/offline_download/task/list"
	OfflineTaskDeletePath = "/offline_download/task/delete"
)

// Envelope is deliberately strict: a missing code is not success.
type Envelope[T any] struct {
	Code      *int   `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
	Data      T      `json:"data"`
}

// LoginData is returned by the sign-in endpoint.
type LoginData struct {
	Token string `json:"token"`
}

// File is the complete metadata needed for identity checks and downloads.
type File struct {
	FileName    string    `json:"FileName"`
	Size        int64     `json:"Size"`
	UpdateAt    time.Time `json:"UpdateAt"`
	FileID      int64     `json:"FileId"`
	Type        int       `json:"Type"`
	ETag        string    `json:"Etag"`
	S3KeyFlag   string    `json:"S3KeyFlag"`
	DownloadURL string    `json:"DownloadUrl"`
}

// IsDir reports the server directory marker.
func (f File) IsDir() bool { return f.Type == 1 }

// FileListData is one page of a directory listing.
type FileListData struct {
	Next     string `json:"Next"`
	Total    int64  `json:"Total"`
	InfoList []File `json:"InfoList"`
}

// UploadData selects rapid, legacy-S3, or presigned upload handling.
type UploadData struct {
	AccessKeyID     string `json:"AccessKeyId"`
	Bucket          string `json:"Bucket"`
	Key             string `json:"Key"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	FileID          int64  `json:"FileId"`
	Reuse           bool   `json:"Reuse"`
	EndPoint        string `json:"EndPoint"`
	StorageNode     string `json:"StorageNode"`
	UploadID        string `json:"UploadId"`
}

// PresignedURLsData maps one-based part numbers to short-lived PUT URLs.
type PresignedURLsData struct {
	PresignedURLs map[string]string `json:"presignedUrls"`
}

// DownloadInfoData contains the first-stage download URL.
type DownloadInfoData struct {
	DownloadURL string `json:"DownloadUrl"`
}

// UserInfoData describes account capacity and identity.
type UserInfoData struct {
	UID            int64  `json:"UID"`
	Nickname       string `json:"Nickname"`
	SpaceUsed      int64  `json:"SpaceUsed"`
	SpacePermanent int64  `json:"SpacePermanent"`
	SpaceTemp      int64  `json:"SpaceTemp"`
	FileCount      int64  `json:"FileCount"`
}

// OfflineResolveData is returned by URL parsing.
type OfflineResolveData struct {
	List []struct {
		Result  int    `json:"result"`
		ID      int64  `json:"id"`
		ErrCode int    `json:"err_code"`
		ErrMsg  string `json:"err_msg"`
		Files   []struct {
			ID int64 `json:"id"`
		} `json:"files"`
	} `json:"list"`
}

// OfflineSubmitData is returned when a resolved URL is queued.
type OfflineSubmitData struct {
	TaskList []struct {
		TaskID int64 `json:"task_id"`
		Result int   `json:"result"`
	} `json:"task_list"`
}

// OfflineTask is the server representation of an offline job.
type OfflineTask struct {
	TaskID     int64   `json:"task_id"`
	Name       string  `json:"name"`
	Status     int     `json:"status"`
	Size       int64   `json:"size"`
	ThirdTask  string  `json:"third_task_id"`
	Downloaded int64   `json:"downloaded"`
	Progress   float64 `json:"progress"`
	UploadID   int64   `json:"upload_idr"`
	UploadName string  `json:"upload_name"`
	Type       string  `json:"type"`
	Speed      int64   `json:"speed"`
}

// OfflineTaskListData is a page of offline jobs.
type OfflineTaskListData struct {
	HasRun bool          `json:"has_run"`
	List   []OfflineTask `json:"list"`
	Total  int64         `json:"total"`
}

// RawEnvelope supports strict status parsing before decoding typed data.
type RawEnvelope = Envelope[json.RawMessage]
