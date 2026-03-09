package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"accounts-sync/internal/client"
	"accounts-sync/internal/k8s"
	"accounts-sync/internal/model"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const cacheDir = "/data/cache"

type RoleService struct {
	httpClient *client.HTTPClient
	baseURL    string
}

var (
	maxWorkspaceConcurrency = 5
	globalQPS               = 10
)

func NewRoleService(c *client.HTTPClient, baseURL string) *RoleService {
	return &RoleService{httpClient: c, baseURL: baseURL}
}

////////////////////////////////////////////////////////////////
// Kubernetes Fetch
////////////////////////////////////////////////////////////////

func (s *RoleService) FetchWorkspaceRoles(ctx context.Context) ([]unstructured.Unstructured, error) {

	client, err := k8s.GetDynamicClient()
	if err != nil {
		return nil, err
	}

	gvr := schema.GroupVersionResource{
		Group:    "iam.kubesphere.io",
		Version:  "v1beta1",
		Resource: "workspaceroles",
	}

	list, err := client.Resource(gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	return list.Items, nil
}

////////////////////////////////////////////////////////////////
// Sync Entry
////////////////////////////////////////////////////////////////

func (s *RoleService) SyncAllWorkspaces(ctx context.Context) error {

	items, err := s.FetchWorkspaceRoles(ctx)
	if err != nil {
		return err
	}

	grouped := s.GroupByWorkspace(items)

	var wg sync.WaitGroup
	sem := make(chan struct{}, 5) // 最大并发 5 个 workspace

	for ws, roles := range grouped {

		wg.Add(1)
		sem <- struct{}{}

		go func(workspace string, r map[string]model.Role) {
			defer wg.Done()
			defer func() { <-sem }()

			log.Println("Sync workspace:", workspace)

			if err := s.SyncOneWorkspace(workspace, r); err != nil {
				log.Println("Workspace sync failed:", workspace, err)
			}
		}(ws, roles)
	}

	wg.Wait()

	return s.CleanupDeletedWorkspaces(grouped)
}

////////////////////////////////////////////////////////////////
// Group
////////////////////////////////////////////////////////////////

func (s *RoleService) GroupByWorkspace(items []unstructured.Unstructured) map[string]map[string]model.Role {

	result := make(map[string]map[string]model.Role)

	for _, item := range items {

		ws := item.GetLabels()["kubesphere.io/workspace"]
		if ws == "" {
			continue
		}

		if _, ok := result[ws]; !ok {
			result[ws] = make(map[string]model.Role)
		}

		name := item.GetName()
		uid := string(item.GetUID())
		workspace := item.GetLabels()["kubesphere.io/workspace"]
		desc := parseZhDescription(
			item.GetAnnotations()["kubesphere.io/description"],
		)
		if desc != "" {
			desc = strings.Replace(desc, "企业空间", "企业空间"+workspace, 1)
		}

		result[ws][name] = model.Role{
			RoleCode:        uid,
			RoleName:        name,
			RoleDescription: desc,
		}
	}

	return result
}

////////////////////////////////////////////////////////////////
// Single Workspace Sync
////////////////////////////////////////////////////////////////

func (s *RoleService) SyncOneWorkspace(workspace string, current map[string]model.Role) error {

	last, err := LoadWorkspaceRoles(workspace)
	if err != nil {
		return err
	}

	toCreate, toUpdate, toDelete :=
		DiffRoles(current, last)

	log.Printf("[%s] create=%d update=%d delete=%d\n",
		workspace, len(toCreate), len(toUpdate), len(toDelete))

	if err := s.ApplyChanges(workspace, toCreate, toUpdate, toDelete); err != nil {
		return err
	}

	return SaveWorkspaceRoles(workspace, current)
}

////////////////////////////////////////////////////////////////
// Diff
////////////////////////////////////////////////////////////////

func DiffRoles(current, last map[string]model.Role) (
	toCreate []model.Role,
	toUpdate []model.Role,
	toDelete []model.Role,
) {

	for code, cur := range current {

		old, exists := last[code]

		if !exists {
			toCreate = append(toCreate, cur)
			continue
		}

		if cur.RoleDescription != old.RoleDescription {
			toUpdate = append(toUpdate, cur)
		}
	}

	for code, old := range last {
		if _, exists := current[code]; !exists {
			toDelete = append(toDelete, old)
		}
	}

	return
}

////////////////////////////////////////////////////////////////
// Cache
////////////////////////////////////////////////////////////////

func cacheFile(ws string) string {
	return filepath.Join(cacheDir, ws+".json")
}

func LoadWorkspaceRoles(ws string) (map[string]model.Role, error) {

	path := cacheFile(ws)

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return make(map[string]model.Role), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var roles map[string]model.Role
	if err := json.Unmarshal(data, &roles); err != nil {
		return nil, err
	}

	return roles, nil
}

func SaveWorkspaceRoles(ws string, roles map[string]model.Role) error {

	path := cacheFile(ws)
	tmp := path + ".tmp"

	data, err := json.MarshalIndent(roles, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}

	return os.Rename(tmp, path)
}

////////////////////////////////////////////////////////////////
// Workspace Delete Cleanup
////////////////////////////////////////////////////////////////

func (s *RoleService) CleanupDeletedWorkspaces(current map[string]map[string]model.Role) error {

	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			return fmt.Errorf("failed to create cache directory: %v", err)
		}
	}

	files, err := os.ReadDir(cacheDir)
	if err != nil {
		return err
	}

	for _, f := range files {

		if !strings.HasSuffix(f.Name(), ".json") {
			continue
		}

		ws := strings.TrimSuffix(f.Name(), ".json")

		if _, exists := current[ws]; !exists {

			log.Println("Workspace deleted:", ws)

			lastRoles, _ := LoadWorkspaceRoles(ws)

			for _, r := range lastRoles {
				s.DeleteRemoteRole(ws, r.RoleCode)
			}

			os.Remove(cacheFile(ws))
		}
	}

	return nil
}

////////////////////////////////////////////////////////////////
// Remote API (示例实现)
////////////////////////////////////////////////////////////////

func (s *RoleService) ApplyChanges(workspace string, create, update, delete []model.Role) error {

	for _, r := range create {
		if err := s.CreateRemoteRole(workspace, r); err != nil {
			return err
		}
	}

	for _, r := range update {
		if err := s.UpdateRemoteRole(workspace, r); err != nil {
			return err
		}
	}

	for _, r := range delete {
		if err := s.DeleteRemoteRole(workspace, r.RoleCode); err != nil {
			return err
		}
	}

	return nil
}

func (s *RoleService) CreateRemoteRole(ws string, r model.Role) error {
	body := model.Role{
		RoleCode:        r.RoleCode,
		RoleName:        r.RoleName,
		RoleDescription: r.RoleDescription,
	}

	log.Println("REMOTE CREATE:", ws, r.RoleCode, body)

	output, _ := json.MarshalIndent(body, "", "  ")
	fmt.Println(string(output))

	return nil
}

func (s *RoleService) UpdateRemoteRole(ws string, r model.Role) error {
	body := model.Role{
		RoleCode:        r.RoleCode,
		RoleName:        r.RoleName,
		RoleDescription: r.RoleDescription,
	}

	log.Println("REMOTE UPDATE:", ws, r.RoleCode)

	output, _ := json.MarshalIndent(body, "", "  ")
	fmt.Println(string(output))

	return nil
}

func (s *RoleService) DeleteRemoteRole(ws, roleCode string) error {
	log.Println("REMOTE DELETE:", ws, roleCode)
	return nil
}

////////////////////////////////////////////////////////////////
// Description Parser
////////////////////////////////////////////////////////////////

func parseZhDescription(raw string) string {

	if raw == "" {
		return ""
	}

	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ""
	}

	desc := m["zh"]
	// 去掉中文句号
	desc = strings.TrimSuffix(desc, "。")

	return desc
}

func (s *RoleService) applyBatch(
	ws string,
	create, update, del []model.Role,
) error {

	if len(create)+len(update) > 0 {

		body := map[string]interface{}{
			"workspace": ws,
			"upsert":    append(create, update...),
		}

		if err := s.httpClient.DoJSON(
			"POST",
			s.baseURL+"/roles/batchUpsert",
			body,
		); err != nil {
			return err
		}
	}

	if len(del) > 0 {

		body := map[string]interface{}{
			"workspace": ws,
			"delete":    del,
		}

		if err := s.httpClient.DoJSON(
			"POST",
			s.baseURL+"/roles/batchDelete",
			body,
		); err != nil {
			return err
		}
	}

	return nil
}
