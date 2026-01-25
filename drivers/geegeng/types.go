package geegeng

import (
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

// 通用 API 响应
type Resp struct {
	Code    int         `json:"code"`
	Message string      `json:"msg"`
	Data    interface{} `json:"data"`
}

// 登录请求
type LoginReq struct {
	Phone    string `json:"phone"`
	Password string `json:"password"`
	Captcha  string `json:"captcha"`
}

// 登录响应
type LoginResp struct {
	Token string `json:"token"`
}

// 文件项
type FileItem struct {
	ID        string `json:"ID"`
	Name      string `json:"name"`
	Type      int    `json:"type"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"CreatedAt"`
}

// 文件列表响应
type FileListResp struct {
	List  []FileItem `json:"list"`
	Total int        `json:"total"`
}

// 下载响应
type DownloadResp struct {
	URL string `json:"url"`
}

// 用户信息
type UserInfo struct {
	ID        string `json:"ID"`
	Store     int64  `json:"store"`     // 总容量
	UsedStore int64  `json:"usedStore"` // 已使用容量
}

// findFile 响应 - 秒传检测
type FindFileResp struct {
	File struct {
		Fid          string `json:"fid"`
		ID           string `json:"ID"` // 备用，防止字段名变化
		Sign         string `json:"sign"`
		UploadStatus bool   `json:"uploadStatus"`
		URL          string `json:"url"` // 上传节点 URL
	} `json:"file"`
}

// initMultiUpload 响应
type InitUploadResp struct {
	UploadID string `json:"uploadId"`
	FileName string `json:"fileName"`
}

// getMultiUploadUrls 响应
type UploadUrlsResp struct {
	UploadUrls map[string]struct {
		RequestUrl string `json:"requestUrl"`
	} `json:"uploadUrls"`
}

// 解析日期字符串
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// GeeCengObject 自定义对象类型，存储 parentId
type GeeCengObject struct {
	model.Object
	ParentID string
}

// GetParentID 返回父目录 ID
func (o *GeeCengObject) GetParentID() string {
	return o.ParentID
}

// 转换为 model.Obj，带 parentId
func fileItemToObj(f FileItem, parentId string) *GeeCengObject {
	return &GeeCengObject{
		Object: model.Object{
			ID:       f.ID,
			Name:     f.Name,
			Size:     f.Size,
			Modified: parseTime(f.CreatedAt),
			IsFolder: f.Type == 2,
		},
		ParentID: parentId,
	}
}
