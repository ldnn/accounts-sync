package main

import (
	"context"
	"log"
	"os"
	"time"

	"accounts-sync/internal/client"
	"accounts-sync/internal/service"
)

func main() {

	start := time.Now()
	defer func() {
		log.Println("job cost:", time.Since(start))
	}()

	appId := os.Getenv("APP_ID")
	appSecret := os.Getenv("APP_SECRET")
	baseURL := os.Getenv("REMOTE_BASE_URL")

	if appId == "" || appSecret == "" {
		log.Fatal("APP_ID or APP_SECRET not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	httpClient := client.NewHTTPClient(50, appId, appSecret)

	roleSvc := service.NewRoleService(httpClient, baseURL)
	accountSvc := service.NewAccountService(httpClient, baseURL)

	if err := roleSvc.SyncAllWorkspaces(ctx); err != nil {
		log.Fatal(err)
	}

	log.Println("roles sync finished")

	if err := accountSvc.SyncAccounts(ctx); err != nil {
		log.Fatal(err)
	}

	log.Println("accounts sync finished")
}
