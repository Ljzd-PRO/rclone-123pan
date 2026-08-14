package _123pan

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/ljzd/rclone-123pan/backend/123pan/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/fserrors"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	uploadChunkSize      = int64(16 * fs.Mebi)
	largeUploadChunkSize = int64(32 * fs.Mebi)
	uploadBatchSize      = int64(10)
	maxUploadParts       = int64(10_000)
)

type partBatch struct {
	start int64
	end   int64
}

type ambiguousCompleteError struct {
	FileID int64
	err    error
}

func (e *ambiguousCompleteError) Error() string {
	return fmt.Sprintf("123Pan upload completion is ambiguous for file ID %d: %s", e.FileID, scrubSecrets(e.err.Error()))
}

func (e *ambiguousCompleteError) Unwrap() error { return e.err }

func uploadPartCount(size, chunkSize int64) (int64, error) {
	if size < 0 {
		return 0, errorsNewProtocol("upload size is unknown")
	}
	if chunkSize != uploadChunkSize && chunkSize != largeUploadChunkSize {
		return 0, errorsNewProtocol("unsupported presigned upload chunk size")
	}
	parts := size / chunkSize
	if size%chunkSize != 0 {
		parts++
	}
	parts = max(parts, 1)
	if parts > maxUploadParts {
		return 0, fmt.Errorf("123Pan presigned upload requires %d parts, maximum is %d", parts, maxUploadParts)
	}
	return parts, nil
}

func uploadBatches(parts int64) []partBatch {
	batchSize := uploadBatchSize
	if parts == 1 {
		batchSize = 1
	}
	result := make([]partBatch, 0, (parts+batchSize-1)/batchSize)
	for start := int64(1); start <= parts; start += batchSize {
		result = append(result, partBatch{start: start, end: min(start+batchSize-1, parts)})
	}
	return result
}

func (f *Fs) getUploadURLs(ctx context.Context, upload api.UploadData, batch partBatch, single bool) (map[string]string, error) {
	request := api.UploadURLRequest{
		StorageNode: upload.StorageNode,
		Bucket:      upload.Bucket,
		Key:         upload.Key,
		// The web API uses an exclusive upper bound even though returned URL
		// keys are one-based part numbers.
		PartNumberEnd:   batch.end + 1,
		PartNumberStart: batch.start,
		UploadID:        upload.UploadID,
	}
	endpoint := api.PresignedPartsPath
	if single {
		endpoint = api.SingleObjectAuthPath
	}
	var data api.PresignedURLsData
	if err := f.client.do(ctx, http.MethodPost, endpoint, request, &data); err != nil {
		return nil, err
	}
	result := make(map[string]string, batch.end-batch.start+1)
	for part := batch.start; part <= batch.end; part++ {
		key := strconv.FormatInt(part, 10)
		raw := data.PresignedURLs[key]
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			return nil, fmt.Errorf("123Pan returned an invalid presigned URL for part %d", part)
		}
		result[key] = u.String()
	}
	return result, nil
}

type uploadURLBatch struct {
	mu      sync.RWMutex
	urls    map[string]string
	refresh singleflight.Group
	batch   partBatch
	single  bool
	upload  api.UploadData
	fs      *Fs
}

func (b *uploadURLBatch) get(part int64) string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.urls[strconv.FormatInt(part, 10)]
}

func (b *uploadURLBatch) refreshURLs(ctx context.Context, part int64, failedURL string) error {
	if b.get(part) != failedURL {
		return nil
	}
	_, err, _ := b.refresh.Do("batch", func() (any, error) {
		if b.get(part) != failedURL {
			return nil, nil
		}
		urls, err := b.fs.getUploadURLs(ctx, b.upload, b.batch, b.single)
		if err != nil {
			return nil, err
		}
		b.mu.Lock()
		b.urls = urls
		b.mu.Unlock()
		return nil, nil
	})
	return err
}

type uploadPart struct {
	number int64
	data   []byte
}

func (f *Fs) putPresignedPart(ctx context.Context, batch *uploadURLBatch, part uploadPart) error {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		retryDelay := time.Second << attempt
		rawURL := batch.get(part.number)
		request, err := http.NewRequestWithContext(ctx, http.MethodPut, rawURL, bytes.NewReader(part.data))
		if err != nil {
			return fmt.Errorf("create upload request for part %d: %w", part.number, err)
		}
		request.ContentLength = int64(len(part.data))
		request.Header.Set("Referer", webOrigin+"/")
		response, err := f.downloadClient.Do(request)
		if err == nil {
			_, copyErr := io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
			closeErr := response.Body.Close()
			if copyErr != nil {
				err = copyErr
			} else if closeErr != nil {
				err = closeErr
			} else if response.StatusCode == http.StatusOK {
				return nil
			} else {
				err = fmt.Errorf("upload part %d returned HTTP %d", part.number, response.StatusCode)
			}
			if delay := parseRetryAfter(response.Header.Get("Retry-After")); delay > 0 {
				retryDelay = delay
			}
			if response.StatusCode == http.StatusForbidden {
				if refreshErr := batch.refreshURLs(ctx, part.number, rawURL); refreshErr != nil {
					return fmt.Errorf("refresh upload URLs for part %d: %w", part.number, refreshErr)
				}
			} else if !fserrors.ShouldRetryHTTP(response, retryHTTPStatuses) {
				return err
			}
		} else if !fserrors.ShouldRetry(err) {
			return err
		}
		last = err
		if attempt < 2 {
			timer := time.NewTimer(retryDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return fmt.Errorf("upload part %d failed after 3 attempts: %w", part.number, last)
}

func readExactPart(reader io.Reader, size, chunkSize int64) ([]byte, error) {
	if size < 0 || size > chunkSize || (chunkSize != uploadChunkSize && chunkSize != largeUploadChunkSize) {
		return nil, errorsNewProtocol("invalid upload part size")
	}
	buffer := make([]byte, size)
	if _, err := io.ReadFull(reader, buffer); err != nil {
		return nil, err
	}
	return buffer, nil
}

func (f *Fs) checkNewMultipartUpload(ctx context.Context, upload api.UploadData) error {
	request := api.UploadPartsRequest{
		StorageNode: upload.StorageNode,
		Bucket:      upload.Bucket,
		Key:         upload.Key,
		UploadID:    upload.UploadID,
	}
	var data api.UploadPartsData
	if err := f.client.do(ctx, http.MethodPost, api.S3ListUploadPartsPath, request, &data); err != nil {
		return err
	}
	parts := bytes.TrimSpace(data.Parts)
	if len(parts) == 0 {
		return errorsNewProtocol("multipart parts response is missing Parts")
	}
	if !bytes.Equal(parts, []byte("null")) {
		return errorsNewProtocol("server returned existing multipart parts; unverified resume is disabled")
	}
	return nil
}

func (f *Fs) uploadPresigned(ctx context.Context, upload api.UploadData, source *preparedSource) (*api.File, error) {
	chunkSize, err := presignedChunkSize(upload)
	if err != nil {
		return nil, err
	}
	parts, err := uploadPartCount(source.size, chunkSize)
	if err != nil {
		return nil, err
	}
	if parts > 1 {
		if err := f.checkNewMultipartUpload(ctx, upload); err != nil {
			return nil, err
		}
	}
	hasher := md5.New()
	reader := io.TeeReader(source.reader, hasher)
	for _, batchRange := range uploadBatches(parts) {
		single := parts == 1
		urls, err := f.getUploadURLs(ctx, upload, batchRange, single)
		if err != nil {
			return nil, err
		}
		batch := &uploadURLBatch{urls: urls, batch: batchRange, single: single, upload: upload, fs: f}
		group, groupCtx := errgroup.WithContext(ctx)
		concurrency := min(int64(f.opt.UploadConcurrency), batchRange.end-batchRange.start+1)
		slots := make(chan struct{}, concurrency)
		var producerErr error
		for number := batchRange.start; number <= batchRange.end; number++ {
			select {
			case <-groupCtx.Done():
				producerErr = groupCtx.Err()
			case slots <- struct{}{}:
			}
			if producerErr != nil {
				break
			}
			offset := (number - 1) * chunkSize
			partSize := min(chunkSize, max(source.size-offset, 0))
			data, readErr := readExactPart(reader, partSize, chunkSize)
			if readErr != nil {
				<-slots
				producerErr = fmt.Errorf("read upload part %d: %w", number, readErr)
				break
			}
			part := uploadPart{number: number, data: data}
			group.Go(func() error {
				defer func() { <-slots }()
				return f.putPresignedPart(groupCtx, batch, part)
			})
		}
		workerErr := group.Wait()
		if producerErr != nil {
			return nil, producerErr
		}
		if workerErr != nil {
			return nil, workerErr
		}
	}
	var extra [1]byte
	if n, err := reader.Read(extra[:]); n != 0 || (err != nil && err != io.EOF) {
		return nil, errorsNewProtocol("source produced more bytes than declared size")
	}
	actualMD5 := hex.EncodeToString(hasher.Sum(nil))
	if actualMD5 != source.md5 {
		return nil, fmt.Errorf("source MD5 changed during upload: declared %s, read %s", source.md5, actualMD5)
	}
	completeRequest := api.UploadCompleteV2Request{
		StorageNode: upload.StorageNode,
		Bucket:      upload.Bucket,
		FileID:      upload.FileID,
		FileSize:    source.size,
		IsMultipart: parts > 1,
		Key:         upload.Key,
		UploadID:    upload.UploadID,
	}
	var complete api.UploadCompleteData
	if err := f.client.doNonIdempotent(ctx, http.MethodPost, api.UploadCompleteV2Path, completeRequest, &complete); err != nil {
		return nil, &ambiguousCompleteError{FileID: upload.FileID, err: err}
	}
	if complete.FileInfo.FileID <= 0 || complete.FileInfo.Size != source.size || normalizeMD5(complete.FileInfo.ETag) != source.md5 || complete.FileInfo.IsDir() {
		return nil, errorsNewProtocol("upload_complete/v2 returned invalid file_info")
	}
	return &complete.FileInfo, nil
}

func (f *Fs) uploadLegacy(ctx context.Context, upload api.UploadData, source *preparedSource) error {
	endpoint, err := parseAbsoluteHTTPURL(upload.EndPoint, "legacy S3 endpoint")
	if err != nil {
		return err
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("123pan"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(upload.AccessKeyID, upload.SecretAccessKey, upload.SessionToken)),
		awsconfig.WithHTTPClient(f.downloadClient),
	)
	if err != nil {
		return fmt.Errorf("configure legacy S3 uploader: %w", err)
	}
	client := s3.NewFromConfig(cfg, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(endpoint.String())
		options.UsePathStyle = true
	})
	uploader := manager.NewUploader(client, func(options *manager.Uploader) {
		options.PartSize = uploadChunkSize
		options.Concurrency = f.opt.UploadConcurrency
		options.LeavePartsOnError = true
	})
	hasher := md5.New()
	_, err = uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(upload.Bucket),
		Key:           aws.String(upload.Key),
		Body:          io.TeeReader(source.reader, hasher),
		ContentLength: aws.Int64(source.size),
	})
	if err != nil {
		return fmt.Errorf("legacy S3 upload failed for file ID %d: %w", upload.FileID, err)
	}
	actualMD5 := hex.EncodeToString(hasher.Sum(nil))
	if actualMD5 != source.md5 {
		return fmt.Errorf("source MD5 changed during legacy upload: declared %s, read %s", source.md5, actualMD5)
	}
	if err := f.client.doNonIdempotent(ctx, http.MethodPost, api.UploadCompletePath, map[string]any{"fileId": upload.FileID}, nil); err != nil {
		return &ambiguousCompleteError{FileID: upload.FileID, err: err}
	}
	return nil
}

func (f *Fs) uploadData(ctx context.Context, upload api.UploadData, source *preparedSource) (*api.File, error) {
	if err := validateUploadDataProfile(upload); err != nil {
		return nil, err
	}
	credentialCount := 0
	for _, value := range []string{upload.AccessKeyID, upload.SecretAccessKey, upload.SessionToken} {
		if value != "" {
			credentialCount++
		}
	}
	switch credentialCount {
	case 0:
		return f.uploadPresigned(ctx, upload, source)
	case 3:
		return nil, f.uploadLegacy(ctx, upload, source)
	default:
		return nil, errorsNewProtocol("upload response contains partial temporary S3 credentials")
	}
}
