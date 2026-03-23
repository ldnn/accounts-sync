package main

import (
	"context"
	"log"
	"time"

	"accounts-sync/internal/client"
	"accounts-sync/internal/service"
	"accounts-sync/internal/config"
)

type App struct {
	roleSvc    *service.RoleService
	accountSvc *service.AccountService
}

func NewApp() *App {
	cfg := config.Load()
	httpClient := client.NewHTTPClient(50, cfg.AppID, cfg.AppSecret)

	return &App{
		roleSvc:    service.NewRoleService(httpClient, cfg.BaseURL),
		accountSvc: service.NewAccountService(httpClient, cfg.BaseURL),
	}
}

func (a *App) Run(ctx context.Context) error {
	if err := a.roleSvc.SyncAllWorkspaces(ctx); err != nil {
		return err
	}

	log.Println("roles sync finished")

	if err := a.accountSvc.SyncAccounts(ctx); err != nil {
		return err
	}

	log.Println("accounts sync finished")
	return nil
}

func main() {
	start := time.Now()
	defer func() {
		log.Println("job cost:", time.Since(start))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	app := NewApp()
	if err := app.Run(ctx); err != nil {
		log.Fatal(err)
	}
}

/*
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
*/
