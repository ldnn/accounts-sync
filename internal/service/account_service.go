package service

import (
	"context"
        "path/filepath"
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"
        "strings"
        "encoding/json"

	"accounts-sync/internal/client"
	"accounts-sync/internal/k8s"
	"accounts-sync/internal/model"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	maxPageSize  = 200
	k8sBatchSize = 1000
	batchFile = "batch.yaml"
)

type AccountService struct {
	httpClient *client.HTTPClient
	baseURL    string
}

func NewAccountService(
	c *client.HTTPClient,
	baseURL string,
) *AccountService {

	return &AccountService{
		httpClient: c,
		baseURL:    baseURL,
	}
}

////////////////////////////////////////////////////////////
// 对外入口
////////////////////////////////////////////////////////////

func (s *AccountService) SyncAccounts(ctx context.Context) error {

	pageNum, pageSize := parseArgs()

	dc, err := k8s.GetDynamicClient()
	if err != nil {
		return err
	}

	userGVR := schema.GroupVersionResource{
		Group:    "iam.kubesphere.io",
		Version:  "v1beta1",
		Resource: "users",
	}

	wrbGVR := schema.GroupVersionResource{
		Group:    "iam.kubesphere.io",
		Version:  "v1beta1",
		Resource: "workspacerolebindings",
	}

	var (
		users []unstructured.Unstructured
		wrbs  []unstructured.Unstructured
		wg    sync.WaitGroup
		errCh = make(chan error, 2)
	)

	wg.Add(2)

	// 并发拉 users
	go func() {
		defer wg.Done()
		items, err := s.listAll(dc, ctx, userGVR)
		if err != nil {
			errCh <- err
			return
		}
		users = items
	}()

	// 并发拉 wrb
	go func() {
		defer wg.Done()
		items, err := s.listAll(dc, ctx, wrbGVR)
		if err != nil {
			errCh <- err
			return
		}
		wrbs = items
	}()

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			panic(err)
		}
	}

	userMap := s.buildAccounts(users)
	s.aggregateRoles(userMap, wrbs)
	s.output(userMap, pageNum, pageSize)

	return nil
}

func parseArgs() (int, int) {

	page := 1
	size := maxPageSize

	if len(os.Args) >= 2 {
		if p, err := strconv.Atoi(os.Args[1]); err == nil && p > 0 {
			page = p
		}
	}

	if len(os.Args) >= 3 {
		if s, err := strconv.Atoi(os.Args[2]); err == nil && s > 0 {
			if s > maxPageSize {
				s = maxPageSize
			}
			size = s
		}
	}

	return page, size
}

// 🔥 K8s 原生分页
func (s *AccountService) listAll(dc dynamic.Interface, ctx context.Context, gvr schema.GroupVersionResource) ([]unstructured.Unstructured, error) {

	var result []unstructured.Unstructured
	continueToken := ""

	for {
		list, err := dc.Resource(gvr).List(ctx, metav1.ListOptions{
			Limit:    k8sBatchSize,
			Continue: continueToken,
		})
		if err != nil {
			return nil, err
		}

		result = append(result, list.Items...)

		if list.GetContinue() == "" {
			break
		}
		continueToken = list.GetContinue()
	}

	return result, nil
}

func (s *AccountService) buildAccounts(items []unstructured.Unstructured) map[string]*model.Account {

	userMap := make(map[string]*model.Account, len(items))

	for _, item := range items {

		annotations := item.GetAnnotations()
		if annotations == nil {
			continue
		}

		casID := annotations["iam.kubesphere.io/identity-provider.cas"]
		if casID == "" {
			continue
		}

		username := item.GetName()

		email, _, _ := unstructured.NestedString(item.Object, "spec", "email")
		if email == "" {
			email = "none"
		}

		forA := annotations["kubesphere.io/alias-name"]
		if forA == "" {
			forA = "none"
		}

		phone := annotations["iam.kubesphere.io/identity-provider.cas"]
		if phone == "" {
			phone = "none"
		}

		activeDate := annotations["identity-syncer.kubesphere.io/sync-time"]
		if activeDate == "" {
			activeDate = "none"
		} else {
			// 如果不为空，进行格式转换
			activeDate = formatTimeToUTCZ(activeDate)
		}

		expire := annotations["identity-syncer.kubesphere.io/account-deadline"]
		if expire == "" {
			expire = lastDate()

		} else {
			// 如果不为空，进行格式转换
			expire = formatTimeToUTCZ(expire)
		}

		state, _, _ := unstructured.NestedString(item.Object, "status", "state")
		enable := state != "Disabled"

		userMap[username] = &model.Account{
			AccountId:   string(item.GetUID()),
			AccountName: username,
			AccountType: "user_technology_account",
			Enable:      enable,
			FourAUser:   forA,
			Email:       email,
			PhoneNumber: phone,
			ActiveDate:  activeDate,
			ExpireDate:  expire,
			Created:     formatTimeToUTCZ(item.GetCreationTimestamp().Time.Format(time.RFC3339)),
			RoleCode:    make([]string, 0, 4), // 预分配
		}
	}

	return userMap
}

// 🔥 Role 去重版本
func (s *AccountService) aggregateRoles(userMap map[string]*model.Account, wrbItems []unstructured.Unstructured) {

	roleCache := make(map[string]map[string]struct{}, len(userMap))

	for _, item := range wrbItems {

		roleName, _, _ := unstructured.NestedString(item.Object, "roleRef", "name")
		subjects, _, _ := unstructured.NestedSlice(item.Object, "subjects")

		for _, s := range subjects {

			subMap, ok := s.(map[string]interface{})
			if !ok {
				continue
			}

			username, ok := subMap["name"].(string)
			if !ok {
				continue
			}

			// 这里只检查是否存在，不声明 user
			if _, exists := userMap[username]; !exists {
				continue
			}

			if roleCache[username] == nil {
				roleCache[username] = make(map[string]struct{}, 4)
			}

			roleCache[username][roleName] = struct{}{}
		}
	}

	// 转换为 slice，并为没有角色的用户设置默认角色
	for username, account := range userMap {
		if roleSet, exists := roleCache[username]; exists && len(roleSet) > 0 {
			// 用户有角色，转换为 slice
			for role := range roleSet {
				account.RoleCode = append(account.RoleCode, role)
			}
		} else {
			// 用户没有角色，设置默认角色
			account.RoleCode = append(account.RoleCode, "no-access")
		}
	}
}

func (s *AccountService) output(userMap map[string]*model.Account, pageNum, pageSize int) {
	// 将map转换为slice
	accounts := make([]model.Account, 0, len(userMap))
	for _, v := range userMap {
		accounts = append(accounts, *v)
	}

	// 按用户名排序
	sort.Slice(accounts, func(i, j int) bool {
		return accounts[i].AccountName < accounts[j].AccountName
	})

	// 计算总页数
	totalCount := len(accounts)
	totalPage := 0
	if totalCount > 0 {
		totalPage = (totalCount + pageSize - 1) / pageSize
	}

	// 如果请求的页码超出范围，设置为最后一页
	if pageNum > totalPage && totalPage != 0 {
		pageNum = totalPage
	}

	
	batchNo, err := NextBatch()
	if err != nil {
		panic(err)
	}

	// 输出所有页的数据
	for page := 1; page <= totalPage; page++ {
		// 计算当前页的起始和结束索引
		start := (page - 1) * pageSize
		end := start + pageSize

		// 确保索引不越界
		if end > totalCount {
			end = totalCount
		}

		// 构建结果
		result := model.AccountsWrapper{
			BatchNo:    batchNo,
			TotalCount: totalCount,
			PageNum:    page,
			TotalPage:  totalPage,
			Accounts:   accounts[start:end],
		}

		// 输出JSON格式结果
		output, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(output))
		// 发送当前页
		if err := s.sendBatch(result); err != nil {
			fmt.Printf("发送第 %d 页失败: %v\n", page, err)
			// 根据需求决定是否重试或停止
			continue
		}

		// 可选：在发送之间加一点延迟，避免请求过快
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *AccountService) sendBatch(
	pageData model.AccountsWrapper,
) error {
	return s.httpClient.DoJSON(
		"POST",
		s.baseURL+"/openapi_v2/scim/AppAccountsUpload",
		pageData,
	)
}

func formatTimeToUTCZ(timeStr string) string {
	if timeStr == "" || timeStr == "none" {
		return timeStr // 保留原样，让后续逻辑处理空值或"none"
	}
	// 尝试解析常见的时间格式，这里以 RFC3339 为例，你的时间看起来符合这个格式
	t, err := time.Parse(time.RFC3339, timeStr)
	if err != nil {
		// 如果解析失败，记录日志并返回原字符串，避免程序崩溃
		// 在实际代码中，可以考虑使用 log.Printf 记录错误
		fmt.Printf("警告: 解析时间字符串 '%s' 失败: %v\n", timeStr, err)
		return timeStr
	}
	// 转换为 UTC 并格式化为 "2021-08-23T04:56:22Z" 形式
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

func lastDate() string {
	now := time.Now().UTC()
	year := now.Year()
	lastDay := time.Date(year+1, 1, 1, 0, 0, 0, 0, time.UTC).
		AddDate(0, 0, -1)
	lastTime := lastDay.Format(time.RFC3339)
        return lastTime
}


func NextBatch() (string, error) {
	path := filepath.Join(cacheDir,"/batch", batchFile)

	// 确保目录存在
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", err
	}

	today := time.Now().Format("20060102") // 如 20260320

	var lastDate string
	var lastBatch int

	data, err := os.ReadFile(path)
	if err == nil {
		parts := strings.Split(strings.TrimSpace(string(data)), ",")
		if len(parts) == 2 {
			lastDate = parts[0]
			lastBatch, _ = strconv.Atoi(parts[1])
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}

	var batch int

	if lastDate == today {
		// 同一天 → +1
		batch = lastBatch + 1
	} else {
		// 新的一天 → 重置为 1
		batch = 1
	}

	// 两位格式
	batchStr := fmt.Sprintf("%s%02d", today,batch)

	// 写入格式：日期,批次号
	content := fmt.Sprintf("%s,%d", today, batch)

	// 原子写入
	tmpFile := path + ".tmp"
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		return "", err
	}

	if err := os.Rename(tmpFile, path); err != nil {
		return "", err
	}

	return batchStr, nil
}
