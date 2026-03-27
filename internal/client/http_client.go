package client

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

type HTTPClient struct {
	client  *http.Client
	limiter <-chan time.Time
	appId   string
	secret  string
}

func NewHTTPClient(qps int, appId, secret string) *HTTPClient {

	return &HTTPClient{
		client: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        500,
				MaxIdleConnsPerHost: 200,
				IdleConnTimeout:     90 * time.Second,
				DialContext: (&net.Dialer{
					Timeout:   15 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				ResponseHeaderTimeout: 45 * time.Second,
			},
		},
		limiter: time.Tick(time.Second / time.Duration(qps)),
		appId:   appId,
		secret:  secret,
	}
}

func (c *HTTPClient) DoJSON(method, url string, body interface{}) ([]byte, error) {
	<-c.limiter

	var err error
	var data []byte
	var req *http.Request

	// 添加重试逻辑，最多重试3次
	for retry := 0; retry < 3; retry++ {
		// 记录请求开始时间
		startTime := time.Now()

		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		if body == nil {
			req, err = http.NewRequest(method, url, nil)
		} else {
			data, err = json.Marshal(body)
			if err != nil {
				return nil, err
			}
			req, err = http.NewRequest(method, url, bytes.NewBuffer(data))
		}

		if err != nil {
			if retry < 2 { // 不是最后一次重试
				time.Sleep(time.Second * time.Duration(retry+1)) // 指数退避：1秒、2秒、3秒
				continue
			}
			return nil, err
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-App-Token", c.generateToken(timestamp))
		req.Header.Set("X-App-Id", c.appId)
		req.Header.Set("X-Timestamp", timestamp)

		fmt.Printf("Sending request to: %s (attempt %d)\n", url, retry+1)
		resp, err := c.client.Do(req)
		if err != nil {
			// 计算并打印请求耗时
			duration := time.Since(startTime)
			fmt.Printf("Request failed after %v\n", duration)

			if retry < 2 { // 不是最后一次重试
				time.Sleep(time.Second * time.Duration(retry+1)) // 指数退避：1秒、2秒、3秒
				continue
			}
			return nil, err
		}

		fmt.Printf("Response Status Code:%v\n", resp.StatusCode)

		defer resp.Body.Close()
		respBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			// 计算并打印请求耗时
			duration := time.Since(startTime)
			fmt.Printf("Request failed after %v\n", duration)

			if retry < 2 { // 不是最后一次重试
				time.Sleep(time.Second * time.Duration(retry+1)) // 指数退避：1秒、2秒、3秒
				continue
			}
			return nil, err
		}

		if resp.StatusCode >= 300 {
			fmt.Printf("Server returned error: %s\n", string(respBytes))

			// 检查是否是批次正在处理中的错误，如果是则不重试
			var errorResp struct {
				ErrorCode string `json:"errorCode"`
				Message   string `json:"message"`
			}
			if err := json.Unmarshal(respBytes, &errorResp); err == nil {
				if errorResp.ErrorCode == "ERROR-SCIM-UPLOAD-0080" {
					// 计算并打印请求耗时
					duration := time.Since(startTime)
					fmt.Printf("Request completed in %v\n", duration)

					return respBytes, fmt.Errorf("remote error: %s", string(respBytes))
				}
			}

			if retry < 2 { // 不是最后一次重试
				time.Sleep(time.Second * time.Duration(retry+1)) // 指数退避：1秒、2秒、3秒
				continue
			}

			// 计算并打印请求耗时
			duration := time.Since(startTime)
			fmt.Printf("Request completed in %v\n", duration)

			return respBytes, fmt.Errorf("remote error: %s", string(respBytes))
		}

		// 计算并打印请求耗时
		duration := time.Since(startTime)
		fmt.Printf("Request completed in %v\n", duration)

		return respBytes, nil
	}

	return nil, fmt.Errorf("request failed after 3 retries")
}

func (c *HTTPClient) generateToken(timestamp string) string {

	data := fmt.Sprintf("%s%s%s", c.appId, c.secret, timestamp)
	hash := sha256Hash(data)
	return hash
}

func sha256Hash(data string) string {
	// 将字符串转换为字节数组
	dataBytes := []byte(data)

	// 使用 SHA-256 创建哈希对象
	hash := sha256.New()

	// 将数据添加到哈希对象中
	hash.Write(dataBytes)

	// 计算 SHA-256 值
	sha256Value := hash.Sum(nil)

	// 将 SHA-256 值转换为十六进制字符串
	sha256Hex := hex.EncodeToString(sha256Value)

	return sha256Hex
}
