package guangyapan

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	RootFolderID string `json:"root_folder_id" help:"根文件夹 ID，留空表示网盘根目录"`
	ClientID     string `json:"client_id" required:"true" help:"填写与 user_agent 端别对应的 client_id"`
	UserAgent    string `json:"user_agent" type:"select" options:"app,web" default:"app" help:"app 为安卓端 UA，web 为网页端 UA"`
	RefreshToken string `json:"refresh_token" required:"true" help:"例如: gy.fcQrJ0xxxxxxxxx"`
	DeviceID     string `json:"device_id" required:"true" help:"例如: feexxxxxxxx"`
	DeviceSign   string `json:"device_sign" ignore:"true"`
	AccessToken  string `json:"access_token" ignore:"true"`
	ExpiresAt    string `json:"expires_at" ignore:"true"`
}

var config = driver.Config{
	Name:              "GuangYaPan",
	DefaultRoot:       "",
	CheckStatus:       true,
	NoOverwriteUpload: true,
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &GuangyaPan{}
	})
}
