package geegeng

import (
	"errors"
	"net/http"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	jsoniter "github.com/json-iterator/go"
)

const (
	loginPath    = "/api/base/login"
	fileListPath = "/api/files/getFilesList"
	downloadPath = "/api/files/downFile"
	mkdirPath    = "/api/files/createFolder"
	deletePath   = "/api/files/deleteFilesByIds"
	renamePath   = "/api/files/updateFileName"
	movePath     = "/api/files/moveFiles"
	copyPath     = "/api/files/copyFiles"
	userInfoPath = "/api/user/getUserInfo"
	// 分片上传相关
	findFilePath        = "/api/files/findFile"
	initMultiUploadPath = "/initMultiUpload"
	getUploadUrlsPath   = "/getMultiUploadUrls"
	commitUploadPath    = "/commitMultiUploadFile"
)

func (d *GeeCeng) getToken() string {
	return d.token
}

func (d *GeeCeng) setToken(token, accessToken string) {
	d.token = token
	d.accessToken = accessToken
}

// 统一请求方法
func (d *GeeCeng) request(method string, path string, callback base.ReqCallback, out interface{}) error {
	u := strings.TrimSuffix(Address, "/") + path
	req := base.RestyClient.R()
	req.SetHeaders(map[string]string{
		"Authorization": "Bearer " + d.getToken(),
		"Accept":        "application/json, text/plain, */*",
		"Content-Type":  "application/json",
		"User-Agent":    base.UserAgent,
		"Referer":       Address + "/",
		"Origin":        Address,
	})

	var r Resp
	req.SetResult(&r)

	if callback != nil {
		callback(req)
	}

	resp, err := req.Execute(method, u)
	if err != nil {
		return err
	}
	if !resp.IsSuccess() {
		return errors.New(resp.String())
	}

	if r.Code != 0 && r.Code != 200 && r.Code != 7 {
		// Token 过期，尝试重新登录
		if r.Code == 401 || r.Code == 403 {
			if path != loginPath {
				err = d.login()
				if err != nil {
					return err
				}
				return d.request(method, path, callback, out)
			}
		}
		return errors.New(r.Message)
	}

	if out != nil && r.Data != nil {
		var marshal []byte
		marshal, err = jsoniter.Marshal(r.Data)
		if err != nil {
			return err
		}
		err = jsoniter.Unmarshal(marshal, out)
		if err != nil {
			return err
		}
	}

	return nil
}

// 登录
func (d *GeeCeng) login() error {
	u := strings.TrimSuffix(Address, "/") + loginPath
	req := base.RestyClient.R()
	req.SetHeaders(map[string]string{
		"Accept":       "application/json, text/plain, */*",
		"Content-Type": "application/json",
		"User-Agent":   base.UserAgent,
		"Referer":      Address + "/login",
		"Origin":       Address,
	})

	loginReq := LoginReq{
		Phone:    d.Username,
		Password: d.Password,
		Captcha:  "",
	}
	req.SetBody(loginReq)

	var r Resp
	req.SetResult(&r)

	resp, err := req.Execute(http.MethodPost, u)
	if err != nil {
		return err
	}
	if !resp.IsSuccess() {
		return errors.New(resp.String())
	}

	if r.Code != 0 && r.Code != 200 {
		return errors.New(r.Message)
	}

	var loginResp LoginResp
	marshal, err := jsoniter.Marshal(r.Data)
	if err != nil {
		return err
	}
	err = jsoniter.Unmarshal(marshal, &loginResp)
	if err != nil {
		return err
	}

	if loginResp.Token == "" {
		return errors.New("login failed: no token received")
	}

	d.setToken(loginResp.Token, loginResp.AccessToken)
	return nil
}

// 获取用户信息（验证 token 有效性）
func (d *GeeCeng) getUserInfo() (*UserInfo, error) {
	var resp struct {
		UserInfo UserInfo `json:"userInfo"`
	}
	err := d.request(http.MethodGet, userInfoPath, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp.UserInfo, nil
}

// 上传节点请求
func (d *GeeCeng) uploadNodeRequest(nodeUrl, path string, formData map[string]string, out interface{}) error {
	u := strings.TrimSuffix(nodeUrl, "/") + path
	req := base.RestyClient.R()
	req.SetHeaders(map[string]string{
		"Accept":     "application/json, text/plain, */*",
		"User-Agent": base.UserAgent,
		"Referer":    Address + "/",
		"Origin":     Address,
	})
	req.SetFormData(formData)

	type NodeResp struct {
		Code int         `json:"code"`
		Msg  string      `json:"msg"`
		Data interface{} `json:"data"`
	}

	var r NodeResp
	req.SetResult(&r)

	resp, err := req.Post(u)
	if err != nil {
		return err
	}
	if !resp.IsSuccess() {
		return errors.New(resp.String())
	}

	if r.Code != 1 && r.Code != 0 {
		return errors.New(r.Msg)
	}

	if out != nil && r.Data != nil {
		marshal, err := jsoniter.Marshal(r.Data)
		if err != nil {
			return err
		}
		err = jsoniter.Unmarshal(marshal, out)
		if err != nil {
			return err
		}
	}

	return nil
}

// 上传节点 GET 请求
func (d *GeeCeng) uploadNodeGet(nodeUrl, path string, params map[string]string, out interface{}) error {
	u := strings.TrimSuffix(nodeUrl, "/") + path
	req := base.RestyClient.R()
	req.SetHeaders(map[string]string{
		"Accept":     "application/json, text/plain, */*",
		"User-Agent": base.UserAgent,
		"Referer":    Address + "/",
		"Origin":     Address,
	})
	req.SetQueryParams(params)

	type NodeResp struct {
		Code int         `json:"code"`
		Msg  string      `json:"msg"`
		Data interface{} `json:"data"`
	}

	var r NodeResp
	req.SetResult(&r)

	resp, err := req.Get(u)
	if err != nil {
		return err
	}
	if !resp.IsSuccess() {
		return errors.New(resp.String())
	}

	if r.Code != 1 && r.Code != 0 {
		return errors.New(r.Msg)
	}

	if out != nil && r.Data != nil {
		marshal, err := jsoniter.Marshal(r.Data)
		if err != nil {
			return err
		}
		err = jsoniter.Unmarshal(marshal, out)
		if err != nil {
			return err
		}
	}

	return nil
}
