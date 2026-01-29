package geegeng

import (
	"bytes"
	"context"
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
	token string
}

// normalizeParentID 将空的 parentId 标准化为 "-1"
func (d *GeeCeng) normalizeParentID(id string) string {
	if id == "" {
		return "-1"
	}
	return id
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
	return nil
}

func (d *GeeCeng) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	parentId := d.normalizeParentID(dir.GetID())

	if (parentId == "-1" || parentId == d.Config().DefaultRoot) && d.RootFolderID != "" {
		parentId = d.RootFolderID
	}

	var allFiles []FileItem
	page := 1
	pageSize := 50

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

		// 添加延迟，避免触发服务器频率限制
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
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
	parentId := d.normalizeParentID(parentDir.GetID())

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
	dstId := d.normalizeParentID(dstDir.GetID())

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
	dstId := d.normalizeParentID(dstDir.GetID())

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

func (d *GeeCeng) GetDetails(ctx context.Context) (*model.StorageDetails, error) {
	userInfo, err := d.getUserInfo()
	if err != nil {
		return nil, fmt.Errorf("failed to get user info: %w", err)
	}

	// 计算可用空间 = 总容量 - 已使用容量
	freeSpace := userInfo.Store - userInfo.UsedStore
	if freeSpace < 0 {
		freeSpace = 0
	}

	return &model.StorageDetails{
		DiskUsage: model.DiskUsage{
			TotalSpace: uint64(userInfo.Store),
			FreeSpace:  uint64(freeSpace),
		},
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
