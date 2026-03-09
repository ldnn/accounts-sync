package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

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
		}

		expire := annotations["identity-syncer.kubesphere.io/account-deadline"]
		if expire == "" {
			expire = "none"

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
			Created:     item.GetCreationTimestamp().Time.Format(time.RFC3339),
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

	// 转换为 slice
	for username, roleSet := range roleCache {
		for role := range roleSet {
			userMap[username].RoleCode = append(userMap[username].RoleCode, role)
		}
	}
}

func (s *AccountService) output(userMap map[string]*model.Account, pageNum, pageSize int) {

	accounts := make([]model.Account, 0, len(userMap))

	for _, v := range userMap {
		accounts = append(accounts, *v)
	}

	sort.Slice(accounts, func(i, j int) bool {
		return accounts[i].AccountName < accounts[j].AccountName
	})

	totalCount := len(accounts)
	totalPage := 0
	if totalCount > 0 {
		totalPage = (totalCount + pageSize - 1) / pageSize
	}

	if pageNum > totalPage && totalPage != 0 {
		pageNum = totalPage
	}

	start := (pageNum - 1) * pageSize
	end := start + pageSize

	if start > totalCount {
		start = totalCount
	}
	if end > totalCount {
		end = totalCount
	}

	result := model.AccountsWrapper{
		TotalCount: totalCount,
		PageNum:    pageNum,
		TotalPage:  totalPage,
		Accounts:   accounts[start:end],
	}

	output, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(output))
}

func (s *AccountService) sendBatch(
	ctx context.Context,
	accounts []model.Account,
) error {

	body := map[string]interface{}{
		"accounts": accounts,
	}

	return s.httpClient.DoJSON(
		"POST",
		s.baseURL+"/accounts/batchUpsert",
		body,
	)
}
