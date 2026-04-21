package guangyapan

import (
	"context"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	streamPkg "github.com/OpenListTeam/OpenList/v4/internal/stream"
	hash_extend "github.com/OpenListTeam/OpenList/v4/pkg/utils/hash"
)

func (d *GuangyaPan) Config() driver.Config {
	return config
}

func (d *GuangyaPan) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *GuangyaPan) Init(ctx context.Context) error {
	did, sign, err := normalizeDevice(d.DeviceID, d.DeviceSign)
	if err != nil {
		return err
	}
	d.DeviceID = did
	d.DeviceSign = sign

	if err := d.ensureAccessToken(ctx); err != nil {
		return err
	}
	if _, err := d.listPage(ctx, d.RootFolderID, 0, 1); err != nil {
		return err
	}
	if d.ID != 0 {
		op.MustSaveDriverStorage(d)
	}
	return nil
}

func (d *GuangyaPan) Drop(ctx context.Context) error {
	return nil
}

func (d *GuangyaPan) GetRoot(ctx context.Context) (model.Obj, error) {
	return &model.Object{
		ID:       d.RootFolderID,
		Path:     "/",
		Name:     "root",
		Modified: d.Modified,
		IsFolder: true,
	}, nil
}

func (d *GuangyaPan) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	page := 0
	result := make([]model.Obj, 0, defaultPageSize)
	for {
		data, err := d.listPage(ctx, dir.GetID(), page, defaultPageSize)
		if err != nil {
			return nil, err
		}
		if len(data.List) == 0 {
			break
		}
		for _, item := range data.List {
			result = append(result, fileToObj(item, dir.GetPath()))
		}
		page++
		if len(data.List) < defaultPageSize || len(result) >= data.Total {
			break
		}
	}
	return result, nil
}

func (d *GuangyaPan) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	data, err := d.getDownloadURL(ctx, file.GetID())
	if err != nil {
		return nil, err
	}
	expiration := time.Duration(data.URLDuration) * time.Second
	return &model.Link{
		URL:        data.SignedURL,
		Expiration: &expiration,
	}, nil
}

func (d *GuangyaPan) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) (model.Obj, error) {
	var data fileInfo
	_, err := d.apiPost(ctx, "/nd.bizuserres.s/v1/file/create_dir", map[string]any{
		"parentId":        parentDir.GetID(),
		"dirName":         dirName,
		"failIfNameExist": true,
	}, &data)
	if err != nil {
		return nil, err
	}
	return fileToObj(data, parentDir.GetPath()), nil
}

func (d *GuangyaPan) Move(ctx context.Context, srcObj, dstDir model.Obj) error {
	var task struct {
		TaskID string `json:"taskId"`
	}
	_, err := d.apiPost(ctx, "/nd.bizuserres.s/v1/file/move_file", map[string]any{
		"fileIds":  []string{srcObj.GetID()},
		"parentId": dstDir.GetID(),
	}, &task)
	if err != nil {
		return err
	}
	return d.waitTask(ctx, task.TaskID)
}

func (d *GuangyaPan) Rename(ctx context.Context, srcObj model.Obj, newName string) (model.Obj, error) {
	_, err := d.apiPost(ctx, "/nd.bizuserres.s/v1/file/rename", map[string]any{
		"fileId":  srcObj.GetID(),
		"newName": newName,
	}, nil)
	if err != nil {
		return nil, err
	}
	return cloneObj(srcObj, srcObj.GetID(), newName, parentObjPath(srcObj)), nil
}

func (d *GuangyaPan) Copy(ctx context.Context, srcObj, dstDir model.Obj) error {
	var task struct {
		TaskID string `json:"taskId"`
	}
	_, err := d.apiPost(ctx, "/nd.bizuserres.s/v1/file/copy_file", map[string]any{
		"fileIds":  []string{srcObj.GetID()},
		"parentId": dstDir.GetID(),
	}, &task)
	if err != nil {
		return err
	}
	return d.waitTask(ctx, task.TaskID)
}

func (d *GuangyaPan) Remove(ctx context.Context, obj model.Obj) error {
	var task struct {
		TaskID string `json:"taskId"`
	}
	_, err := d.apiPost(ctx, "/nd.bizuserres.s/v1/file/delete_file", map[string]any{
		"fileIds": []string{obj.GetID()},
	}, &task)
	if err != nil {
		return err
	}
	return d.waitTask(ctx, task.TaskID)
}

func (d *GuangyaPan) Put(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	code, token, err := d.getUploadToken(ctx, dstDir.GetID(), stream)
	if err != nil {
		return nil, err
	}

	shouldUpload := code != 156
	uploadProgress := up
	if shouldUpload {
		gcid := stream.GetHash().GetHash(hash_extend.GCID)
		if len(gcid) < hash_extend.GCID.Width {
			hashProgress := model.UpdateProgressWithRange(up, 0, 5)
			uploadProgress = model.UpdateProgressWithRange(up, 5, 100)
			_, gcid, err = streamPkg.CacheFullAndHash(stream, &hashProgress, hash_extend.GCID, stream.GetSize())
			if err != nil {
				d.deleteUploadTask(context.WithoutCancel(ctx), token.TaskID)
				return nil, err
			}
		}

		canFlashUpload, flashErr := d.checkCanFlashUpload(ctx, token.TaskID, gcid)
		if flashErr == nil && canFlashUpload {
			shouldUpload = false
		}
	}

	if shouldUpload {
		if err := d.uploadObject(ctx, stream, uploadProgress, token); err != nil {
			d.deleteUploadTask(context.WithoutCancel(ctx), token.TaskID)
			return nil, err
		}
	}

	info, err := d.waitUploadTask(ctx, token.TaskID)
	if err != nil {
		if ctx.Err() != nil {
			d.deleteUploadTask(context.WithoutCancel(ctx), token.TaskID)
		}
		return nil, err
	}
	up(100)

	if fresh, err := d.getInfoByFileID(ctx, info.FileID); err == nil {
		info = fresh
	}
	return fileToObj(*info, dstDir.GetPath()), nil
}

func (d *GuangyaPan) GetDetails(ctx context.Context) (*model.StorageDetails, error) {
	data, err := d.getAssets(ctx)
	if err != nil {
		return nil, err
	}
	return &model.StorageDetails{
		DiskUsage: model.DiskUsage{
			TotalSpace: data.TotalSpaceSize,
			UsedSpace:  data.UsedSpaceSize,
		},
	}, nil
}

var _ driver.Driver = (*GuangyaPan)(nil)
var _ driver.GetRooter = (*GuangyaPan)(nil)
var _ driver.MkdirResult = (*GuangyaPan)(nil)
var _ driver.Move = (*GuangyaPan)(nil)
var _ driver.RenameResult = (*GuangyaPan)(nil)
var _ driver.Copy = (*GuangyaPan)(nil)
var _ driver.Remove = (*GuangyaPan)(nil)
var _ driver.PutResult = (*GuangyaPan)(nil)
var _ driver.WithDetails = (*GuangyaPan)(nil)
