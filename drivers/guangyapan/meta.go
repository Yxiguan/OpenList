package guangyapan

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Addition struct {
	RootFolderID string `json:"root_folder_id" help:"根文件夹 ID，留空表示网盘根目录"`
	ClientID     string `json:"client_id" required:"true" help:"例如: aMe_eFSlkrbQXpUV"`
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
