package geegeng

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/go-resty/resty/v2"
)

const (
	chunkSize  = 10 * 1024 * 1024 // 10MB 分片
	maxRetries = 3                // 最大重试次数
)

// 分片 buffer 池，复用内存减少 GC
var chunkPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, chunkSize)
	},
}

func (d *GeeCeng) Put(ctx context.Context, dstDir model.Obj, file model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	parentId := d.normalizeParentID(dstDir.GetID())

	fileSize := file.GetSize()
	fileName := file.GetName()

	// 1. 获取可 Seek 的文件（缓存到临时文件，不占用内存）
	tempFile, err := file.CacheFullAndWriter(&up, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to cache file: %w", err)
	}

	// 2. 流式计算 MD5（只分配小 buffer）
	fileMd5, err := d.computeFileMD5Stream(tempFile)
	if err != nil {
		return nil, fmt.Errorf("failed to compute MD5: %w", err)
	}

	// Seek 回开头准备上传
	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek file: %w", err)
	}

	// 3. 调用 findFile 检查秒传
	timestamp := time.Now().UnixNano() / 1e6

	var findResp FindFileResp
	err = d.request(http.MethodGet, findFilePath, func(req *resty.Request) {
		req.SetQueryParams(map[string]string{
			"name":     fileName,
			"md5":      fileMd5,
			"size":     fmt.Sprintf("%d", fileSize),
			"parentId": parentId,
			"category": "4",
			"path":     fileName,
			"t":        fmt.Sprintf("%d", timestamp),
		})
	}, &findResp)
	if err != nil {
		return nil, fmt.Errorf("findFile failed: %w", err)
	}

	// 如果 uploadStatus=true 表示秒传成功
	if findResp.File.UploadStatus {
		up(100)
		return d.buildUploadResult(ctx, findResp, fileName, fileSize, parentId, file.ModTime())
	}

	// 获取用户信息以得到 uid
	userInfo, err := d.getUserInfo()
	if err != nil {
		return nil, fmt.Errorf("failed to get user info: %w", err)
	}

	nodeUrl := findResp.File.URL
	fid := findResp.File.Fid
	sign := findResp.File.Sign

	// 4. 初始化分片上传
	var initResp InitUploadResp
	err = d.doNodeRequest(http.MethodPost, nodeUrl, initMultiUploadPath, map[string]string{
		"name": fileName,
		"size": fmt.Sprintf("%d", fileSize),
		"md5":  fileMd5,
		"sign": sign,
		"fid":  fid,
		"salt": fileMd5[:16],
		"uid":  userInfo.ID,
	}, &initResp)
	if err != nil {
		return nil, fmt.Errorf("initMultiUpload failed: %w", err)
	}

	uploadId := initResp.UploadID
	serverFileName := initResp.FileName

	// 5. 分片上传（从临时文件读取，内存只占用单个分片大小）
	totalChunks := (int(fileSize) + chunkSize - 1) / chunkSize

	for i := 0; i < totalChunks; i++ {
		if utils.IsCanceled(ctx) {
			return nil, ctx.Err()
		}

		// 从池中获取 buffer
		buf := chunkPool.Get().([]byte)

		// 从临时文件读取分片
		n, err := io.ReadFull(tempFile, buf)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			chunkPool.Put(buf)
			return nil, fmt.Errorf("read chunk %d failed: %w", i+1, err)
		}
		chunk := buf[:n]

		// 计算分片 MD5
		chunkHash := md5.Sum(chunk)
		chunkMd5 := hex.EncodeToString(chunkHash[:])

		// 上传分片（带重试）
		err = d.uploadChunkWithRetry(ctx, nodeUrl, serverFileName, uploadId, i+1, chunk, chunkMd5)
		chunkPool.Put(buf) // 归还 buffer

		if err != nil {
			return nil, err
		}

		// 更新进度
		progress := float64(i+1) * 100 / float64(totalChunks)
		up(progress)
	}

	// 6. 提交完成上传
	err = d.doNodeRequest(http.MethodPost, nodeUrl, commitUploadPath, map[string]string{
		"fileName": serverFileName,
		"uploadId": uploadId,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("commitMultiUploadFile failed: %w", err)
	}

	up(100)
	return &model.Object{
		ID:       fid,
		Name:     fileName,
		Size:     fileSize,
		Modified: file.ModTime(),
		IsFolder: false,
	}, nil
}

// computeFileMD5Stream 流式计算 MD5，只使用小 buffer
func (d *GeeCeng) computeFileMD5Stream(file io.Reader) (string, error) {
	hash := md5.New()
	buf := make([]byte, 32*1024) // 32KB buffer

	for {
		n, err := file.Read(buf)
		if n > 0 {
			hash.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// uploadChunkWithRetry 带重试的分片上传
func (d *GeeCeng) uploadChunkWithRetry(ctx context.Context, nodeUrl, serverFileName, uploadId string, partNum int, chunk []byte, chunkMd5 string) error {
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if utils.IsCanceled(ctx) {
			return ctx.Err()
		}

		err := d.uploadSingleChunk(ctx, nodeUrl, serverFileName, uploadId, partNum, chunk, chunkMd5)
		if err == nil {
			return nil
		}

		lastErr = err
		// 指数退避：1s, 2s, 4s
		if attempt < maxRetries-1 {
			time.Sleep(time.Duration(1<<attempt) * time.Second)
		}
	}

	return fmt.Errorf("upload part %d failed after %d retries: %w", partNum, maxRetries, lastErr)
}

// uploadSingleChunk 上传单个分片
func (d *GeeCeng) uploadSingleChunk(ctx context.Context, nodeUrl, serverFileName, uploadId string, partNum int, chunk []byte, chunkMd5 string) error {
	// 构建 partsInfo
	partInfo := fmt.Sprintf("%d-%s", partNum, chunkMd5)
	partsInfo := base64.StdEncoding.EncodeToString([]byte(partInfo))

	// 获取上传 URL
	var urlsResp UploadUrlsResp
	err := d.doNodeRequest(http.MethodGet, nodeUrl, getUploadUrlsPath, map[string]string{
		"fileName":  serverFileName,
		"uploadId":  uploadId,
		"partsInfo": partsInfo,
	}, &urlsResp)
	if err != nil {
		return fmt.Errorf("getMultiUploadUrls failed: %w", err)
	}

	partKey := fmt.Sprintf("partNumber_%d", partNum)
	uploadUrl, ok := urlsResp.UploadUrls[partKey]
	if !ok {
		return fmt.Errorf("upload URL not found for part %d", partNum)
	}

	// 上传分片
	fullUrl := nodeUrl + uploadUrl.RequestUrl
	resp, err := base.RestyClient.R().
		SetContext(ctx).
		SetBody(chunk).
		SetHeader("Content-Type", "application/octet-stream").
		Put(fullUrl)
	if err != nil {
		return err
	}
	if !resp.IsSuccess() {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode(), resp.String())
	}

	return nil
}

// buildUploadResult 构建秒传成功后的返回对象
func (d *GeeCeng) buildUploadResult(ctx context.Context, findResp FindFileResp, fileName string, fileSize int64, parentId string, modTime time.Time) (*model.Object, error) {
	finalID := findResp.File.Fid
	if finalID == "" {
		finalID = findResp.File.ID
	}
	if finalID == "" {
		// 尝试通过列表获取 ID
		parentObj := &model.Object{ID: parentId}
		files, err := d.List(ctx, parentObj, model.ListArgs{})
		if err == nil {
			for _, f := range files {
				if f.GetName() == fileName {
					finalID = f.GetID()
					break
				}
			}
		}
	}
	if finalID == "" {
		finalID = "0"
	}

	return &model.Object{
		ID:       finalID,
		Name:     fileName,
		Size:     fileSize,
		Modified: modTime,
		IsFolder: false,
	}, nil
}
