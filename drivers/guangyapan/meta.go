package guangyapan

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	RootFolderID string `json:"root_folder_id" help:"根文件夹 ID，留空表示网盘根目录"`
	RefreshToken string `json:"refresh_token" required:"true" help:"浏览器抓包或 localStorage 中的 refresh_token"`
	DeviceID     string `json:"device_id" required:"true" help:"浏览器请求头中的 x-device-id，或 32 位 did"`
	DeviceSign   string `json:"device_sign" ignore:"true"`
	AccessToken  string `json:"access_token" ignore:"true"`
	ExpiresAt    string `json:"expires_at" ignore:"true"`
}

var config = driver.Config{
	Name:              "GuangYaPan",
	DefaultRoot:       "",
	CheckStatus:       true,
	NoOverwriteUpload: true,
	Alert:             "info|填写浏览器里的 refresh_token 和 x-device-id 即可，access_token / expires_at 为内部缓存字段，无需填写",
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &GuangyaPan{}
	})
}
