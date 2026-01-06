package geegeng

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

const Address = "https://www.geegeng.com"

type Addition struct {
	RootFolderID string `json:"根目录ID"`
	Username     string `json:"username" required:"true"`
	Password     string `json:"password" required:"true"`
}

func (a Addition) GetRootId() string {
	return a.RootFolderID
}

var config = driver.Config{
	Name:        "桔梗网盘",
	LocalSort:   true,
	DefaultRoot: "-1",
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &GeeCeng{}
	})
}
