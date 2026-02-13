package geegeng

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/mitchellh/mapstructure"
)

const (
	loginPath       = "/api/base/login"
	loginStatusPath = "/api/base/getBaseInfo"
	fileListPath    = "/api/files/getFilesList"
	downloadPath    = "/api/files/downFile"
	mkdirPath       = "/api/files/createFolder"
	deletePath      = "/api/files/deleteFilesByIds"
	renamePath      = "/api/files/updateFileName"
	movePath        = "/api/files/moveFiles"
	copyPath        = "/api/files/copyFiles"
	userInfoPath    = "/api/user/getUserInfo"
	// 分片上传相关
	findFilePath        = "/api/files/findFile"
	initMultiUploadPath = "/initMultiUpload"
	getUploadUrlsPath   = "/getMultiUploadUrls"
	commitUploadPath    = "/commitMultiUploadFile"
)

func (d *GeeCeng) getToken() string {
	return d.token
}

func (d *GeeCeng) setToken(token string) {
	d.token = token
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
		decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
			Result:  out,
			TagName: "json",
		})
		if err != nil {
			return err
		}
		if err := decoder.Decode(r.Data); err != nil {
			return err
		}
	}

	return nil
}

// PKCS7 填充
func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padtext := make([]byte, padding)
	for i := range padtext {
		padtext[i] = byte(padding)
	}
	return append(data, padtext...)
}

// PKCS7 去填充
func pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty data")
	}
	padding := int(data[len(data)-1])
	if padding > len(data) || padding == 0 {
		return nil, errors.New("invalid padding")
	}
	return data[:len(data)-padding], nil
}

// AES-CBC 加密 (key 和 iv 相同，与前端逻辑一致)
func aesEncryptCBC(plainText, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	padded := pkcs7Pad(plainText, block.BlockSize())
	encrypted := make([]byte, len(padded))
	mode := cipher.NewCBCEncrypter(block, key) // iv = key
	mode.CryptBlocks(encrypted, padded)
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

// AES-CBC 解密 (key 和 iv 相同，与前端逻辑一致)
func aesDecryptCBC(cipherB64 string, key []byte) (string, error) {
	cipherBytes, err := base64.StdEncoding.DecodeString(cipherB64)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	if len(cipherBytes)%block.BlockSize() != 0 {
		return "", errors.New("ciphertext is not a multiple of the block size")
	}
	mode := cipher.NewCBCDecrypter(block, key) // iv = key
	mode.CryptBlocks(cipherBytes, cipherBytes)
	result, err := pkcs7Unpad(cipherBytes)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

// 获取登录状态（获取密码加密所需的 info 和 salt）
func (d *GeeCeng) getLoginStatus() (*LoginStatusResp, error) {
	var resp LoginStatusResp
	err := d.request(http.MethodGet, loginStatusPath, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// 登录
func (d *GeeCeng) login() error {
	// 1. 获取加密密钥信息
	status, err := d.getLoginStatus()
	if err != nil {
		return errors.New("failed to get login status: " + err.Error())
	}

	// 2. Base64 解码 salt 得到 AES 密钥
	saltKey, err := base64.StdEncoding.DecodeString(status.Salt)
	if err != nil {
		return errors.New("failed to decode salt: " + err.Error())
	}

	// 3. 双重解密 info 得到真正的密码加密密钥
	// step1 = AES_CBC_Decrypt(info, saltKey, saltKey)
	step1, err := aesDecryptCBC(status.Info, saltKey)
	if err != nil {
		return errors.New("failed to decrypt info (step1): " + err.Error())
	}
	// realKey = AES_CBC_Decrypt(step1, saltKey, saltKey)
	realKeyStr, err := aesDecryptCBC(step1, saltKey)
	if err != nil {
		return errors.New("failed to decrypt info (step2): " + err.Error())
	}

	// 4. 用 realKey 加密密码
	encryptedPwd, err := aesEncryptCBC([]byte(d.Password), []byte(realKeyStr))
	if err != nil {
		return errors.New("failed to encrypt password: " + err.Error())
	}

	// 5. 发送登录请求
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
		Password: encryptedPwd,
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
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:  &loginResp,
		TagName: "json",
	})
	if err != nil {
		return err
	}
	if err := decoder.Decode(r.Data); err != nil {
		return err
	}

	if loginResp.Token == "" {
		return errors.New("login failed: no token received")
	}

	d.setToken(loginResp.Token)
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

// 统一的上传节点请求方法
func (d *GeeCeng) doNodeRequest(method, nodeUrl, path string, data map[string]string, out interface{}) error {
	u := strings.TrimSuffix(nodeUrl, "/") + path
	req := base.RestyClient.R()
	req.SetHeaders(map[string]string{
		"Accept":     "application/json, text/plain, */*",
		"User-Agent": base.UserAgent,
		"Referer":    Address + "/",
		"Origin":     Address,
	})

	// 根据 method 设置参数
	if method == http.MethodGet {
		req.SetQueryParams(data)
	} else {
		req.SetFormData(data)
	}

	type NodeResp struct {
		Code int         `json:"code"`
		Msg  string      `json:"msg"`
		Data interface{} `json:"data"`
	}

	var r NodeResp
	req.SetResult(&r)

	resp, err := req.Execute(method, u)
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
		decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
			Result:  out,
			TagName: "json",
		})
		if err != nil {
			return err
		}
		if err := decoder.Decode(r.Data); err != nil {
			return err
		}
	}

	return nil
}
