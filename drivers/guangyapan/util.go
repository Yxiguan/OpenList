package guangyapan

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	stdpath "path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	netutil "github.com/OpenListTeam/OpenList/v4/internal/net"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	streamPkg "github.com/OpenListTeam/OpenList/v4/internal/stream"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	hash_extend "github.com/OpenListTeam/OpenList/v4/pkg/utils/hash"
	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"github.com/avast/retry-go"
)

const (
	apiBaseURL       = "https://api.guangyapan.com"
	accountBaseURL   = "https://account.guangyapan.com"
	sdkVersion       = "9.0.2"
	protocolVersion  = "301"
	clientVersion    = "0.0.1"
	defaultPageSize  = 200
	androidUserAgent = "Dalvik/2.1.0 (Linux; U; Android 13; 23013RK75C Build/TKQ1.221114.001)"
	androidModel     = "23013RK75C"
	androidDevice    = "Xiaomi-23013RK75C"
	androidOSVersion = "Android 13"
)

var didPattern = regexp.MustCompile(`(?i)(?:wdi10\.)?([0-9a-f]{32})`)

func (d *GuangyaPan) normalizeClientID() (string, error) {
	clientID := strings.TrimSpace(d.ClientID)
	if clientID == "" {
		return "", fmt.Errorf("missing client id")
	}
	d.ClientID = clientID
	return clientID, nil
}

func (d *GuangyaPan) parseExpiresAt() (time.Time, error) {
	if strings.TrimSpace(d.ExpiresAt) == "" {
		return time.Time{}, fmt.Errorf("empty expires_at")
	}
	if tm, err := time.Parse(time.RFC3339Nano, d.ExpiresAt); err == nil {
		return tm, nil
	}
	return time.Parse(time.RFC3339, d.ExpiresAt)
}

func (d *GuangyaPan) tokenExpired() bool {
	if strings.TrimSpace(d.AccessToken) == "" {
		return true
	}
	expiresAt, err := d.parseExpiresAt()
	if err != nil {
		return true
	}
	return time.Now().Add(2 * time.Minute).After(expiresAt)
}

func normalizeDevice(deviceID, deviceSign string) (string, string, error) {
	deviceID = strings.TrimSpace(deviceID)
	deviceSign = strings.TrimSpace(deviceSign)

	if deviceSign == "" && strings.HasPrefix(strings.ToLower(deviceID), "wdi10.") {
		deviceSign = deviceID
	}
	if deviceID == "" && deviceSign != "" {
		deviceID = deviceSign
	}

	matches := didPattern.FindStringSubmatch(strings.ToLower(deviceID))
	if len(matches) < 2 && deviceSign != "" {
		matches = didPattern.FindStringSubmatch(strings.ToLower(deviceSign))
	}
	if len(matches) < 2 {
		return "", "", fmt.Errorf("invalid device id")
	}

	did := matches[1]
	if deviceSign == "" {
		deviceSign = "wdi10." + did + "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
	}
	return did, deviceSign, nil
}

func newTraceParent() string {
	traceID := make([]byte, 16)
	spanID := make([]byte, 8)
	if _, err := rand.Read(traceID); err != nil {
		panic(err)
	}
	if _, err := rand.Read(spanID); err != nil {
		panic(err)
	}
	return "00-" + hex.EncodeToString(traceID) + "-" + hex.EncodeToString(spanID) + "-01"
}

func (d *GuangyaPan) ensureAccessToken(ctx context.Context) error {
	if !d.tokenExpired() {
		return nil
	}
	return d.refreshAccessToken(ctx)
}

func (d *GuangyaPan) refreshAccessToken(ctx context.Context) error {
	d.refreshMu.Lock()
	defer d.refreshMu.Unlock()

	if !d.tokenExpired() {
		return nil
	}

	if strings.TrimSpace(d.RefreshToken) == "" {
		return fmt.Errorf("missing refresh token")
	}
	clientID, err := d.normalizeClientID()
	if err != nil {
		return err
	}

	var resp accountTokenResp
	req := base.RestyClient.R()
	req.SetContext(ctx)
	req.SetHeader("Content-Type", "application/json")
	req.SetHeader("User-Agent", androidUserAgent)
	req.SetHeader("x-action", "401")
	req.SetHeader("x-client-id", clientID)
	req.SetHeader("x-client-version", clientVersion)
	req.SetHeader("x-device-id", d.DeviceID)
	req.SetHeader("x-device-model", androidModel)
	req.SetHeader("x-device-name", androidDevice)
	req.SetHeader("x-device-sign", d.DeviceSign)
	req.SetHeader("x-net-work-type", "WIFI")
	req.SetHeader("x-os-version", androidOSVersion)
	req.SetHeader("x-platform-version", "1")
	req.SetHeader("x-provider-name", "NONE")
	req.SetHeader("x-protocol-version", protocolVersion)
	req.SetHeader("x-sdk-version", sdkVersion)
	req.SetBody(base.Json{
		"client_id":     clientID,
		"client_secret": "",
		"grant_type":    "refresh_token",
		"refresh_token": d.RefreshToken,
	})
	req.SetResult(&resp)

	res, err := req.Post(accountBaseURL + "/v1/auth/token")
	if err != nil {
		return err
	}
	if res.IsError() {
		return fmt.Errorf("refresh token failed: %s", res.Status())
	}
	if strings.TrimSpace(resp.AccessToken) == "" {
		return fmt.Errorf("refresh token failed: empty access token")
	}

	d.AccessToken = resp.AccessToken
	if resp.RefreshToken != "" {
		d.RefreshToken = resp.RefreshToken
	}
	if resp.ExpiresIn > 0 {
		d.ExpiresAt = time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	}
	if d.ID != 0 {
		op.MustSaveDriverStorage(d)
	}
	return nil
}

func (d *GuangyaPan) apiPost(ctx context.Context, path string, body any, out any, allowedCodes ...int) (int, error) {
	if err := d.ensureAccessToken(ctx); err != nil {
		return 0, err
	}
	return d.apiPostWithRetry(ctx, path, body, out, false, allowedCodes...)
}

func (d *GuangyaPan) apiPostWithRetry(ctx context.Context, path string, body any, out any, refreshed bool, allowedCodes ...int) (int, error) {
	req := base.RestyClient.R()
	req.SetContext(ctx)
	req.SetHeader("Authorization", "Bearer "+d.AccessToken)
	req.SetHeader("Content-Type", "application/json")
	req.SetHeader("User-Agent", androidUserAgent)
	req.SetHeader("dt", "4")
	req.SetHeader("did", d.DeviceID)
	req.SetHeader("traceparent", newTraceParent())
	req.SetBody(body)

	res, err := req.Post(apiBaseURL + path)
	if err != nil {
		return 0, err
	}
	if res.StatusCode() == http.StatusUnauthorized && !refreshed {
		d.AccessToken = ""
		d.ExpiresAt = ""
		if err := d.refreshAccessToken(ctx); err != nil {
			return 0, err
		}
		return d.apiPostWithRetry(ctx, path, body, out, true, allowedCodes...)
	}

	var envelope responseEnvelope
	if err := json.Unmarshal(res.Body(), &envelope); err != nil {
		return 0, err
	}

	code := envelope.Code
	if code == 0 || slices.Contains(allowedCodes, code) {
		if out != nil && len(envelope.Data) > 0 && string(envelope.Data) != "null" {
			if err := json.Unmarshal(envelope.Data, out); err != nil {
				return code, err
			}
		}
		return code, nil
	}

	if code == 159 {
		return code, errs.ObjectAlreadyExists
	}
	if res.IsError() && strings.TrimSpace(envelope.Msg) == "" {
		return code, fmt.Errorf("request failed: %s", res.Status())
	}

	msg := strings.TrimSpace(envelope.Msg)
	if msg == "" {
		msg = "request failed"
	}
	return code, fmt.Errorf("%s (code=%d)", msg, code)
}

func (d *GuangyaPan) listPage(ctx context.Context, parentID string, page int, pageSize int) (*fileListData, error) {
	var data fileListData
	_, err := d.apiPost(ctx, "/nd.bizuserres.s/v1/file/get_file_list", base.Json{
		"parentId":  parentID,
		"page":      page,
		"pageSize":  pageSize,
		"orderBy":   3,
		"sortType":  1,
		"fileTypes": []int{},
	}, &data)
	if err != nil {
		return nil, err
	}
	return &data, nil
}

func (d *GuangyaPan) getDownloadURL(ctx context.Context, fileID string) (*downloadData, error) {
	var data downloadData
	_, err := d.apiPost(ctx, "/nd.bizuserres.s/v1/get_res_download_url", base.Json{
		"fileId": fileID,
	}, &data)
	if err != nil {
		return nil, err
	}
	return &data, nil
}

func (d *GuangyaPan) getInfoByFileID(ctx context.Context, fileID string) (*fileInfo, error) {
	var data fileInfo
	_, err := d.apiPost(ctx, "/nd.bizuserres.s/v1/file/get_info_by_file_id", base.Json{
		"fileId": fileID,
	}, &data)
	if err != nil {
		return nil, err
	}
	return &data, nil
}

func (d *GuangyaPan) getAssets(ctx context.Context) (*assetsData, error) {
	var data assetsData
	_, err := d.apiPost(ctx, "/nd.bizassets.s/v1/get_assets", base.Json{}, &data)
	if err != nil {
		return nil, err
	}
	return &data, nil
}

func (d *GuangyaPan) checkCanFlashUpload(ctx context.Context, taskID, gcid string) (bool, error) {
	taskID = strings.TrimSpace(taskID)
	gcid = strings.TrimSpace(gcid)
	if taskID == "" || gcid == "" {
		return false, nil
	}

	var data flashUploadData
	_, err := d.apiPost(ctx, "/nd.bizuserres.s/v1/check_can_flash_upload", base.Json{
		"taskId": taskID,
		"gcid":   strings.ToUpper(gcid),
	}, &data)
	if err != nil {
		return false, err
	}
	return data.CanFlashUpload, nil
}

func (d *GuangyaPan) waitTask(ctx context.Context, taskID string) error {
	const (
		maxAttempts = 120
		interval    = time.Second
	)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		var data taskStatusData
		_, err := d.apiPost(ctx, "/nd.bizuserres.s/v1/get_task_status", base.Json{
			"taskId": taskID,
		}, &data)
		if err != nil {
			return err
		}

		if data.Detail != nil && data.Detail.Code != 0 && (data.Status == 2 || data.Status == 3) {
			return fmt.Errorf("%s (code=%d)", data.Detail.Msg, data.Detail.Code)
		}

		switch data.Status {
		case 2:
			return nil
		case 3:
			if data.Detail != nil && data.Detail.Msg != "" {
				return fmt.Errorf("%s", data.Detail.Msg)
			}
			return fmt.Errorf("task failed")
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("task timeout")
}

func (d *GuangyaPan) waitUploadTask(ctx context.Context, taskID string) (*fileInfo, error) {
	const (
		maxAttempts = 180
		interval    = time.Second
	)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		var data fileInfo
		code, err := d.apiPost(ctx, "/nd.bizuserres.s/v1/file/get_info_by_task_id", base.Json{
			"taskId": taskID,
		}, &data, 145, 146, 147, 155, 163)
		if err == nil && data.FileID != "" {
			return &data, nil
		}
		if err != nil && !slices.Contains([]int{145, 146, 147, 155, 163}, code) {
			return nil, err
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, fmt.Errorf("upload task timeout")
}

func (d *GuangyaPan) getUploadToken(ctx context.Context, parentID string, stream model.FileStreamer) (int, *uploadTokenData, error) {
	var data uploadTokenData
	code, err := d.apiPost(ctx, "/nd.bizuserres.s/v1/get_res_center_token", base.Json{
		"capacity": 2,
		"name":     stream.GetName(),
		"res": base.Json{
			"fileSize": stream.GetSize(),
		},
		"parentId": parentID,
	}, &data, 156)
	if err != nil {
		return code, nil, err
	}
	return code, &data, nil
}

func (d *GuangyaPan) resumeUploadToken(ctx context.Context, streamSize int64, current *uploadTokenData) (*uploadTokenData, error) {
	var data uploadTokenData
	_, err := d.apiPost(ctx, "/nd.bizuserres.s/v1/get_res_center_resume_token", base.Json{
		"capacity": 2,
		"res": base.Json{
			"fileSize": streamSize,
		},
		"taskId": current.TaskID,
		"object": base.Json{
			"objectPath": current.ObjectPath,
			"provider":   current.Provider,
		},
	}, &data, 156)
	if err != nil {
		return nil, err
	}
	return &data, nil
}

func (d *GuangyaPan) deleteUploadTask(ctx context.Context, taskID string) {
	if strings.TrimSpace(taskID) == "" {
		return
	}
	_, _ = d.apiPost(ctx, "/nd.bizuserres.s/v1/file/delete_upload_task", base.Json{
		"taskIds": []string{taskID},
	}, nil)
}

func (d *GuangyaPan) credentialsExpiring(expiration string) bool {
	if strings.TrimSpace(expiration) == "" {
		return false
	}
	tm, err := time.Parse(time.RFC3339, expiration)
	if err != nil {
		return false
	}
	return time.Now().Add(time.Minute).After(tm)
}

func newOSSBucket(token *uploadTokenData) (*oss.Bucket, error) {
	endpoint := strings.TrimSpace(token.EndPoint)
	if endpoint == "" {
		return nil, fmt.Errorf("missing oss endpoint")
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "https://" + endpoint
	}
	client, err := netutil.NewOSSClient(
		endpoint,
		token.Creds.AccessKeyID,
		token.Creds.SecretAccessKey,
		oss.SecurityToken(token.Creds.SessionToken),
	)
	if err != nil {
		return nil, err
	}
	return client.Bucket(token.BucketName)
}

const (
	guangyaWebSmallUploadLimit  int64 = 100 * utils.MB
	guangyaWebMediumUploadLimit int64 = 1 * utils.GB
	guangyaWebLargeUploadLimit  int64 = 10 * utils.GB
	guangyaWebSmallPartSize     int64 = 1 * utils.MB
	guangyaWebMediumPartSize    int64 = 2 * utils.MB
	guangyaWebLargePartSize     int64 = 4 * utils.MB
	guangyaWebXLargePartSize    int64 = 8 * utils.MB
)

func calcPartSize(fileSize int64) int64 {
	// Mirror the current web uploader's chunk sizing strategy.
	switch {
	case fileSize <= 0:
		return guangyaWebSmallPartSize
	case fileSize <= guangyaWebSmallUploadLimit:
		return guangyaWebSmallPartSize
	case fileSize <= guangyaWebMediumUploadLimit:
		return guangyaWebMediumPartSize
	case fileSize <= guangyaWebLargeUploadLimit:
		return guangyaWebLargePartSize
	default:
		return guangyaWebXLargePartSize
	}
}

func (d *GuangyaPan) uploadObject(ctx context.Context, stream model.FileStreamer, up driver.UpdateProgress, token *uploadTokenData) error {
	fileSize := stream.GetSize()
	if fileSize <= 0 {
		bucket, err := newOSSBucket(token)
		if err != nil {
			return err
		}
		if err := bucket.PutObject(token.ObjectPath, driver.NewLimitedUploadStream(ctx, bytes.NewReader(nil))); err != nil {
			return err
		}
		up(100)
		return nil
	}

	partSize := calcPartSize(fileSize)
	if fileSize <= partSize {
		bucket, err := newOSSBucket(token)
		if err != nil {
			return err
		}
		reader := driver.NewLimitedUploadStream(ctx, &driver.ReaderUpdatingProgress{
			Reader: &driver.SimpleReaderWithSize{
				Reader: stream,
				Size:   fileSize,
			},
			UpdateProgress: up,
		})
		return bucket.PutObject(token.ObjectPath, reader)
	}

	bucket, err := newOSSBucket(token)
	if err != nil {
		return err
	}
	imur, err := bucket.InitiateMultipartUpload(token.ObjectPath, oss.Sequential())
	if err != nil {
		return err
	}

	abortUpload := true
	defer func() {
		if abortUpload {
			_ = bucket.AbortMultipartUpload(imur)
		}
	}()

	ss, err := streamPkg.NewStreamSectionReader(stream, int(partSize), nil)
	if err != nil {
		return err
	}

	partCount := (fileSize + partSize - 1) / partSize
	parts := make([]oss.UploadPart, partCount)
	currentToken := token
	offset := int64(0)

	for i := int64(1); i <= partCount; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.credentialsExpiring(currentToken.Creds.Expiration) {
			currentToken, err = d.resumeUploadToken(ctx, fileSize, currentToken)
			if err != nil {
				return err
			}
			bucket, err = newOSSBucket(currentToken)
			if err != nil {
				return err
			}
		}

		currentSize := partSize
		if i == partCount {
			currentSize = fileSize - offset
		}

		section, err := ss.GetSectionReader(offset, currentSize)
		if err != nil {
			return err
		}

		partStart := float64(offset) * 100 / float64(fileSize)
		partEnd := float64(offset+currentSize) * 100 / float64(fileSize)
		err = retry.Do(func() error {
			if _, seekErr := section.Seek(0, io.SeekStart); seekErr != nil {
				return seekErr
			}
			part, uploadErr := bucket.UploadPart(
				imur,
				driver.NewLimitedUploadStream(ctx, &driver.ReaderUpdatingProgress{
					Reader: &driver.SimpleReaderWithSize{
						Reader: section,
						Size:   currentSize,
					},
					UpdateProgress: model.UpdateProgressWithRange(up, partStart, partEnd),
				}),
				currentSize,
				int(i),
			)
			if uploadErr != nil {
				return uploadErr
			}
			parts[i-1] = part
			return nil
		},
			retry.Context(ctx),
			retry.Attempts(3),
			retry.Delay(time.Second),
			retry.DelayType(retry.BackOffDelay),
		)
		ss.FreeSectionReader(section)
		if err != nil {
			return err
		}
		offset += currentSize
	}

	if _, err := bucket.CompleteMultipartUpload(imur, parts); err != nil {
		return err
	}
	abortUpload = false
	return nil
}

func joinObjPath(parentPath, name string) string {
	if parentPath == "" || parentPath == "/" {
		return stdpath.Join("/", name)
	}
	return stdpath.Join(parentPath, name)
}

func parentObjPath(obj model.Obj) string {
	dir := stdpath.Dir(obj.GetPath())
	if dir == "." {
		return "/"
	}
	return dir
}

func cloneObj(src model.Obj, newID, newName, parentPath string) model.Obj {
	if newID == "" {
		newID = src.GetID()
	}
	if newName == "" {
		newName = src.GetName()
	}
	return &model.Object{
		ID:       newID,
		Name:     newName,
		Path:     joinObjPath(parentPath, newName),
		Size:     src.GetSize(),
		Modified: src.ModTime(),
		Ctime:    src.CreateTime(),
		IsFolder: src.IsDir(),
		HashInfo: src.GetHash(),
	}
}

func fileToObj(item fileInfo, parentPath string) model.Obj {
	var hashInfo utils.HashInfo
	hashMap := make(map[*utils.HashType]string)
	if item.MD5 != "" {
		hashMap[utils.MD5] = strings.ToLower(item.MD5)
	}
	if item.GCID != "" {
		hashMap[hash_extend.GCID] = strings.ToUpper(item.GCID)
	}
	if len(hashMap) > 0 {
		hashInfo = utils.NewHashInfoByMap(hashMap)
	}

	var modTime time.Time
	var createTime time.Time
	if item.UTime > 0 {
		modTime = time.Unix(item.UTime, 0)
	}
	if item.CTime > 0 {
		createTime = time.Unix(item.CTime, 0)
	}

	return &model.Object{
		ID:       item.FileID,
		Name:     item.FileName,
		Path:     joinObjPath(parentPath, item.FileName),
		Size:     item.FileSize,
		Modified: modTime,
		Ctime:    createTime,
		IsFolder: item.ResType == 2,
		HashInfo: hashInfo,
	}
}
