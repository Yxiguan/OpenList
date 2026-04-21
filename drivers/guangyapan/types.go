package guangyapan

import (
	"encoding/json"
	"sync"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

type GuangyaPan struct {
	model.Storage
	Addition

	refreshMu sync.Mutex
}

type responseEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

type accountTokenResp struct {
	TokenType    string `json:"token_type"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Sub          string `json:"sub"`
}

type fileListData struct {
	Total int        `json:"total"`
	List  []fileInfo `json:"list"`
}

type fileInfo struct {
	FileID      string `json:"fileId"`
	FileName    string `json:"fileName"`
	FileSize    int64  `json:"fileSize"`
	GCID        string `json:"gcid"`
	MD5         string `json:"md5"`
	Depth       int    `json:"depth"`
	MineType    string `json:"mineType"`
	FileType    int    `json:"fileType"`
	DirType     int    `json:"dirType"`
	ResType     int    `json:"resType"`
	Ext         string `json:"ext"`
	CTime       int64  `json:"ctime"`
	UTime       int64  `json:"utime"`
	AuditStatus int    `json:"auditStatus"`
}

type downloadData struct {
	SignedURL   string `json:"signedURL"`
	URLDuration int64  `json:"urlDuration"`
	RequestID   string `json:"requestId"`
}

type taskStatusData struct {
	Status int         `json:"status"`
	Detail *taskDetail `json:"detail"`
}

type taskDetail struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

type uploadTokenData struct {
	GCID         string      `json:"gcid"`
	Provider     int         `json:"provider"`
	Creds        uploadCreds `json:"creds"`
	EndPoint     string      `json:"endPoint"`
	BucketName   string      `json:"bucketName"`
	ObjectPath   string      `json:"objectPath"`
	CallbackVar  string      `json:"callbackVar"`
	Region       string      `json:"region"`
	TaskID       string      `json:"taskId"`
	FullEndPoint string      `json:"fullEndPoint"`
}

type uploadCreds struct {
	AccessKeyID     string `json:"accessKeyID"`
	SecretAccessKey string `json:"secretAccessKey"`
	SessionToken    string `json:"sessionToken"`
	Expiration      string `json:"expiration"`
}

type flashUploadData struct {
	CanFlashUpload bool   `json:"canFlashUpload"`
	TaskID         string `json:"taskId"`
}

type assetsData struct {
	TotalSpaceSize int64 `json:"totalSpaceSize"`
	UsedSpaceSize  int64 `json:"usedSpaceSize"`
}
