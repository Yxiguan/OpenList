package geegeng

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/http_range"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/go-resty/resty/v2"
)

type GeeCeng struct {
	model.Storage
	Addition
	token       string
	accessToken string
}

func (d *GeeCeng) Config() driver.Config {
	return config
}

func (d *GeeCeng) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *GeeCeng) Init(ctx context.Context) error {
	// 登录获取 token
	err := d.login()
	if err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	// 验证 token
	_, err = d.getUserInfo()
	if err != nil {
		return fmt.Errorf("get user info failed: %w", err)
	}

	return nil
}

func (d *GeeCeng) Drop(ctx context.Context) error {
	d.token = ""
	d.accessToken = ""
	return nil
}

func (d *GeeCeng) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	parentId := dir.GetID()
	if parentId == "" {
		parentId = "-1"
	}

	if (parentId == "-1" || parentId == d.Config().DefaultRoot) && d.RootFolderID != "" {
		parentId = d.RootFolderID
	}

	var allFiles []FileItem
	page := 1
	pageSize := 100

	for {
		var fileList FileListResp
		path := fmt.Sprintf("%s?parentId=%s&page=%d&pageSize=%d", fileListPath, parentId, page, pageSize)
		err := d.request(http.MethodGet, path, nil, &fileList)
		if err != nil {
			return nil, err
		}

		allFiles = append(allFiles, fileList.List...)

		if len(fileList.List) < pageSize || len(allFiles) >= fileList.Total {
			break
		}
		page++
	}

	return utils.SliceConvert(allFiles, func(src FileItem) (model.Obj, error) {
		return fileItemToObj(src, parentId), nil
	})
}

func (d *GeeCeng) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	if file.GetSize() == 0 {
		return &model.Link{
			RangeReader: &EmptyReader{},
		}, nil
	}

	var downloadResp DownloadResp
	err := d.request(http.MethodPost, downloadPath, func(req *resty.Request) {
		req.SetBody(base.Json{
			"id": file.GetID(),
		})
	}, &downloadResp)
	if err != nil {
		return nil, err
	}

	if downloadResp.URL == "" {
		return nil, errs.ObjectNotFound
	}

	return &model.Link{
		URL: downloadResp.URL,
		Header: http.Header{
			"Referer":    []string{Address + "/"},
			"User-Agent": []string{base.UserAgent},
			"Origin":     []string{Address},
		},
	}, nil
}

func (d *GeeCeng) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) (model.Obj, error) {
	parentId := parentDir.GetID()
	if parentId == "" {
		parentId = "-1"
	}

	// API 响应可能返回新创建的目录信息
	var result FileItem
	err := d.request(http.MethodPost, mkdirPath, func(req *resty.Request) {
		req.SetBody(base.Json{
			"parentId": parentId,
			"name":     dirName,
		})
	}, &result)
	if err != nil {
		return nil, err
	}

	// 如果 API 返回了 ID，使用它；否则返回 nil 强制刷新
	if result.ID != "" {
		return fileItemToObj(result, parentId), nil
	}

	// 返回 nil 让系统刷新列表
	return nil, nil
}

func (d *GeeCeng) Move(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	dstId := dstDir.GetID()
	if dstId == "" {
		dstId = "-1"
	}

	err := d.request(http.MethodPost, movePath, func(req *resty.Request) {
		req.SetBody(base.Json{
			"ids":      []string{srcObj.GetID()},
			"parentId": dstId,
		})
	}, nil)
	if err != nil {
		return nil, err
	}

	// 返回移动后的对象
	return &model.Object{
		ID:       srcObj.GetID(),
		Name:     srcObj.GetName(),
		Size:     srcObj.GetSize(),
		Modified: srcObj.ModTime(),
		IsFolder: srcObj.IsDir(),
	}, nil
}

func (d *GeeCeng) Rename(ctx context.Context, srcObj model.Obj, newName string) (model.Obj, error) {
	// 获取 parentId
	parentId := "-1"
	if gcObj, ok := srcObj.(*GeeCengObject); ok {
		parentId = gcObj.GetParentID()
	}

	err := d.request(http.MethodPost, renamePath, func(req *resty.Request) {
		req.SetBody(base.Json{
			"ID":       srcObj.GetID(),
			"name":     newName,
			"parentId": parentId,
		})
	}, nil)
	if err != nil {
		return nil, err
	}

	// 返回重命名后的对象
	return &GeeCengObject{
		Object: model.Object{
			ID:       srcObj.GetID(),
			Name:     newName,
			Size:     srcObj.GetSize(),
			Modified: srcObj.ModTime(),
			IsFolder: srcObj.IsDir(),
		},
		ParentID: parentId,
	}, nil
}

func (d *GeeCeng) Copy(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	dstId := dstDir.GetID()
	if dstId == "" {
		dstId = "-1"
	}

	err := d.request(http.MethodPost, copyPath, func(req *resty.Request) {
		req.SetBody(base.Json{
			"ids":      []string{srcObj.GetID()},
			"parentId": dstId,
		})
	}, nil)
	if err != nil {
		return nil, err
	}

	// 返回复制后的对象
	return &model.Object{
		Name:     srcObj.GetName(),
		Size:     srcObj.GetSize(),
		Modified: srcObj.ModTime(),
		IsFolder: srcObj.IsDir(),
	}, nil
}

func (d *GeeCeng) Remove(ctx context.Context, obj model.Obj) error {
	return d.request(http.MethodPost, deletePath, func(req *resty.Request) {
		req.SetBody(base.Json{
			"ids": []string{obj.GetID()},
		})
	}, nil)
}

func (d *GeeCeng) Put(ctx context.Context, dstDir model.Obj, file model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	parentId := dstDir.GetID()
	if parentId == "" {
		parentId = "-1"
	}

	// 读取文件内容用于计算 MD5
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// 计算 MD5
	hash := md5.Sum(content)
	fileMd5 := hex.EncodeToString(hash[:])
	fileSize := int64(len(content))

	// 1. 调用 findFile 检查秒传
	timestamp := time.Now().UnixNano() / 1e6

	var findResp FindFileResp
	err = d.request(http.MethodGet, findFilePath, func(req *resty.Request) {
		req.SetQueryParams(map[string]string{
			"name":     file.GetName(),
			"md5":      fileMd5,
			"size":     fmt.Sprintf("%d", fileSize),
			"parentId": parentId,
			"category": "4",
			"path":     file.GetName(),
			"t":        fmt.Sprintf("%d", timestamp),
		})
	}, &findResp)
	if err != nil {
		return nil, fmt.Errorf("findFile failed: %w", err)
	}

	// 如果 uploadStatus=true 表示秒传成功
	if findResp.File.UploadStatus {
		up(100)
		finalID := findResp.File.Fid
		if finalID == "" {
			finalID = findResp.File.ID
		}
		if finalID == "" {
			// 尝试通过列表获取 ID
			// 构造一个临时的父目录对象传给 List
			parentObj := &model.Object{ID: parentId}
			// 获取文件列表（假设刚上传的文件在前 100 个中）
			files, err := d.List(ctx, parentObj, model.ListArgs{})
			if err == nil {
				for _, f := range files {
					if f.GetName() == file.GetName() {
						finalID = f.GetID()
						break
					}
				}
			}
		}

		// 如果仍然为空，生成一个假的 ID 防止 panic，但 link 会失败
		if finalID == "" {
			finalID = "0" // 避免 ParseInt 空字符串错误，虽然 Link 可能会 404
		}

		return &model.Object{
			ID:       finalID,
			Name:     file.GetName(),
			Size:     fileSize,
			Modified: file.ModTime(),
			IsFolder: false,
		}, nil
	}

	// 获取用户信息以得到 uid
	userInfo, err := d.getUserInfo()
	if err != nil {
		return nil, fmt.Errorf("failed to get user info: %w", err)
	}
	uid := userInfo.ID

	nodeUrl := findResp.File.URL
	fid := findResp.File.Fid
	sign := findResp.File.Sign

	// 2. 初始化分片上传
	var initResp InitUploadResp
	err = d.uploadNodeRequest(nodeUrl, initMultiUploadPath, map[string]string{
		"name": file.GetName(),
		"size": fmt.Sprintf("%d", fileSize),
		"md5":  fileMd5,
		"sign": sign,
		"fid":  fid,
		"salt": fileMd5[:16], // 使用 MD5 前 16 位作为 salt
		"uid":  uid,
	}, &initResp)
	if err != nil {
		return nil, fmt.Errorf("initMultiUpload failed: %w", err)
	}

	uploadId := initResp.UploadID
	serverFileName := initResp.FileName

	// 3. 分片上传
	const chunkSize = 5 * 1024 * 1024 // 5MB 分片
	totalChunks := (int(fileSize) + chunkSize - 1) / chunkSize

	for i := 0; i < totalChunks; i++ {
		if utils.IsCanceled(ctx) {
			return nil, ctx.Err()
		}

		start := i * chunkSize
		end := start + chunkSize
		if end > int(fileSize) {
			end = int(fileSize)
		}
		chunk := content[start:end]

		// 计算分片 MD5
		chunkHash := md5.Sum(chunk)
		chunkMd5 := hex.EncodeToString(chunkHash[:])

		// 构建 partsInfo (Base64 编码的 "partNumber-md5")
		partInfo := fmt.Sprintf("%d-%s", i+1, chunkMd5)
		partsInfo := base64.StdEncoding.EncodeToString([]byte(partInfo))

		// 获取上传 URL
		var urlsResp UploadUrlsResp
		err = d.uploadNodeGet(nodeUrl, getUploadUrlsPath, map[string]string{
			"fileName":  serverFileName,
			"uploadId":  uploadId,
			"partsInfo": partsInfo,
		}, &urlsResp)
		if err != nil {
			return nil, fmt.Errorf("getMultiUploadUrls failed: %w", err)
		}

		partKey := fmt.Sprintf("partNumber_%d", i+1)
		uploadUrl, ok := urlsResp.UploadUrls[partKey]
		if !ok {
			return nil, fmt.Errorf("upload URL not found for part %d", i+1)
		}

		// 上传分片
		fullUrl := nodeUrl + uploadUrl.RequestUrl
		resp, err := base.RestyClient.R().
			SetContext(ctx).
			SetBody(chunk).
			SetHeader("Content-Type", "application/octet-stream").
			Put(fullUrl)
		if err != nil {
			return nil, fmt.Errorf("upload part %d failed: %w", i+1, err)
		}
		if !resp.IsSuccess() {
			return nil, fmt.Errorf("upload part %d failed with status: %d", i+1, resp.StatusCode())
		}

		// 更新进度
		progress := float64(i+1) * 100 / float64(totalChunks)
		up(progress)
	}

	// 4. 提交完成上传
	err = d.uploadNodeRequest(nodeUrl, commitUploadPath, map[string]string{
		"fileName": serverFileName,
		"uploadId": uploadId,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("commitMultiUploadFile failed: %w", err)
	}

	up(100)
	return &model.Object{
		ID:       fid,
		Name:     file.GetName(),
		Size:     fileSize,
		Modified: file.ModTime(),
		IsFolder: false,
	}, nil
}

var _ driver.Driver = (*GeeCeng)(nil)
var _ driver.MkdirResult = (*GeeCeng)(nil)
var _ driver.MoveResult = (*GeeCeng)(nil)
var _ driver.RenameResult = (*GeeCeng)(nil)
var _ driver.CopyResult = (*GeeCeng)(nil)
var _ driver.Remove = (*GeeCeng)(nil)
var _ driver.PutResult = (*GeeCeng)(nil)

type EmptyReader struct{}

func (r *EmptyReader) RangeRead(ctx context.Context, httpRange http_range.Range) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}
