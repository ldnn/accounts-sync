package client

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
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
			Timeout: 20 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        500,
				MaxIdleConnsPerHost: 200,
				IdleConnTimeout:     90 * time.Second,
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
			},
		},
		limiter: time.Tick(time.Second / time.Duration(qps)),
		appId:   appId,
		secret:  secret,
	}
}

func (c *HTTPClient) DoJSON(method, url string, body interface{}) error {

	<-c.limiter

	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	var lastErr error

	for i := 0; i < 4; i++ {
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)

		req, _ := http.NewRequest(method, url, bytes.NewBuffer(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-App-Token", c.generateToken(timestamp))
		req.Header.Set("X-App-Id", c.appId)
		req.Header.Set("X-Timestamp", timestamp)

		resp, err := c.client.Do(req)
		if err == nil && resp.StatusCode < 500 {

			defer resp.Body.Close()

			if resp.StatusCode >= 300 {
				b, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("remote error: %s", string(b))
			}
			return nil
		}

		if resp != nil {
			resp.Body.Close()
		}

		lastErr = err
		time.Sleep(time.Duration(math.Pow(2, float64(i))) * time.Second)
	}

	return fmt.Errorf("request failed: %w", lastErr)
}

func (c *HTTPClient) generateToken(timestamp string) string {

	data := fmt.Sprintf("%s%s%s", c.appId, c.secret, timestamp)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
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
