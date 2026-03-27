package service

import (
	"accounts-syncer/internal/client"
	"accounts-syncer/internal/k8s"
	"accounts-syncer/internal/model"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

type StateSyncService struct {
	httpClient *client.HTTPClient
	baseURL    string
}

func NewStateSyncService(c *client.HTTPClient, baseURL string) *StateSyncService {
	return &StateSyncService{httpClient: c, baseURL: baseURL}
}

func (s *StateSyncService) AccountsStateSync(ctx context.Context) error {

	var diffs []model.ResourceAccount

	lastBatch, err := getLastBatch()
	if err != nil {
		return err
	}
	diff, err := s.getAccountDiff(1, lastBatch)
	if err != nil {
		return err
	}
	if diff.TotalResults == 0 {
		return nil
	}
	if index := (diff.TotalResults + maxPageSize - 1) / maxPageSize; index > 1 {
		diffs = append(diffs, diff.Resources...)
		for i := 2; i <= index; i++ {
			pageDiff, err := s.getAccountDiff(i, lastBatch)
			if err != nil {
				return err
			}
			diffs = append(diffs, pageDiff.Resources...)
		}
	} else {
		diffs = diff.Resources
	}
	//fmt.Print(diffs)

	s.handleDiff(diffs)
	return nil
}

func getLastBatch() (string, error) {

	cacheDir := filepath.Join(dataDir, "/batch")

	path := filepath.Join(cacheDir, batchFile)

	// 读取文件
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
	return fmt.Sprintf("%s%02d", lastDate, lastBatch), nil
}

func (s *StateSyncService) getAccountDiff(index int, batch string) (model.AppAccountDiffResponse, error) {

	var respBytes []byte
	u, err := url.Parse(s.baseURL + "/openapi_v2/scim/AppAccountDiff")
	if err != nil {
		return model.AppAccountDiffResponse{}, err
	}

	q := u.Query()
	q.Set("filter", fmt.Sprintf(`batchNo eq "%s"`, batch))
	q.Set("count", strconv.Itoa(maxPageSize))
	q.Set("startIndex", strconv.Itoa(index))
	u.RawQuery = q.Encode()

	respBytes, err = s.httpClient.DoJSON(
		"GET",
		u.String(),
		nil,
	)
	if err != nil {
		return model.AppAccountDiffResponse{}, err
	}

	var diffs model.AppAccountDiffResponse
	if err := json.Unmarshal(respBytes, &diffs); err != nil {
		return model.AppAccountDiffResponse{}, err
	}

	return diffs, nil

}

func (s *StateSyncService) handleDiff(resources []model.ResourceAccount) {
	for _, r := range resources {

		switch r.DiffType {

		case "ACCOUNT_MORE":
			fmt.Printf("Deleting KubeSphere user %s:\n", r.AccountName)
			/*
				err := s.deleteKubeSphereUser(r.AccountName)
				if err != nil {
					fmt.Printf("Error deleting KubeSphere user %s: %v\n", r.AccountName, err)
				}
			*/

		case "ROLE_NOT_REPORTED":
			fmt.Printf("Update role for KubeSphere user %s:\n", r.AccountName)
			err := s.updateAccountRole(r.AccountName, r.RoleCode)
			if err != nil {
				fmt.Printf("Error update role for KubeSphere user %s: %v\n", r.AccountName, err)
			}

		case "STATUS_EXPIRE_DIFF":
			if !r.Enable {
				fmt.Printf("Sync KubeSphere user %s state: %v\n", r.AccountName, r.Enable)
				err := s.syncKubeSphereUserState(r.AccountName, r.Enable)
				if err != nil {
					fmt.Printf("Error sync KubeSphere user %s state: %v\n", r.AccountName, err)
				}

			}
			if r.ExpireDate != "" {
				fmt.Printf("Sync KubeSphere user %s Expire: %v\n", r.AccountName, r.ExpireDate)
				err := s.syncKubeSphereUserExpire(r.AccountName, r.ExpireDate)
				if err != nil {
					fmt.Printf("Error sync KubeSphere user %s Expire: %v\n", r.AccountName, err)
				}

			}
		}
	}
}

func (s *StateSyncService) updateAccountRole(userName string, roles []string) error {
	fmt.Println("角色未备案:", userName, roles)
	return nil
}

func (s *StateSyncService) syncAccountStatus(iamAccount model.AppAccount) {
	fmt.Println("同步状态和过期时间:", iamAccount.AccountName, iamAccount.Enable, iamAccount.ExpireDate)
}

func (s *StateSyncService) deleteKubeSphereUser(username string) error {
	dc, err := k8s.GetDynamicClient()
	if err != nil {
		return err
	}

	userGVR := schema.GroupVersionResource{
		Group:    "iam.kubesphere.io",
		Version:  "v1beta1",
		Resource: "users",
	}

	ctx := context.Background()

	// 1. 获取 User
	user, err := dc.Resource(userGVR).Get(ctx, username, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// 2. 删除 finalizers
	unstructured.SetNestedStringSlice(
		user.Object,
		[]string{},
		"metadata",
		"finalizers",
	)

	_, err = dc.Resource(userGVR).Update(ctx, user, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	// 3. 删除 User
	err = dc.Resource(userGVR).Delete(ctx, username, metav1.DeleteOptions{})
	if err != nil {
		return err
	}

	return nil
}

func (s *StateSyncService) syncKubeSphereUserState(username string, enable bool) error {
	dc, err := k8s.GetDynamicClient()
	if err != nil {
		return err
	}

	userGVR := schema.GroupVersionResource{
		Group:    "iam.kubesphere.io",
		Version:  "v1beta1",
		Resource: "users",
	}

	state := "Active"
	if !enable {
		state = "Disabled"
	}

	patch := []byte(`{
		"status": {
			"state": "` + state + `"
		}
	}`)

	_, err = dc.Resource(userGVR).Patch(
		context.Background(),
		username,
		types.MergePatchType,
		patch,
		metav1.PatchOptions{},
		"status",
	)

	return err
}

func (s *StateSyncService) syncKubeSphereUserExpire(username string, expireDate string) error {
	dc, err := k8s.GetDynamicClient()
	if err != nil {
		return err
	}

	userGVR := schema.GroupVersionResource{
		Group:    "iam.kubesphere.io",
		Version:  "v1beta1",
		Resource: "users",
	}

	patch := []byte(`{
		"metadata": {
			"annotations": {
				"kubesphere.io/expiredate": "` + expireDate + `"
			}
		}
	}`)

	_, err = dc.Resource(userGVR).Patch(
		context.Background(),
		username,
		types.MergePatchType,
		patch,
		metav1.PatchOptions{},
	)

	return err
}
