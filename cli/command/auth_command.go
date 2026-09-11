package command

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"codebuddy-gateway/model"
	"codebuddy-gateway/service"

	"github.com/spf13/cobra"
)

func NewAuthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Get CodeBuddy credentials via official login",
	}
	cmd.AddCommand(newAuthLoginCommand())
	return cmd
}

func newAuthLoginCommand() *cobra.Command {
	var (
		platform  string
		timeout   int
		interval  int
		noBrowser bool
		noSave    bool
		outPath   string
		name      string
	)
	cmd := &cobra.Command{
		Use:          "login",
		Short:        "Open CodeBuddy login page, then save the token locally",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			Bootstrap(cmd)
			client := service.NewUpstreamClient()
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
			defer cancel()

			fmt.Println("[1/3] requesting login URL...")
			session, err := client.StartDeviceAuth(ctx, platform)
			if err != nil {
				return err
			}
			fmt.Printf("state: %s\n", session.State)
			fmt.Printf("open this URL and finish login:\n%s\n", session.AuthURL)
			if !noBrowser {
				if err := openBrowser(session.AuthURL); err != nil {
					fmt.Fprintf(os.Stderr, "could not open browser: %v\n", err)
				}
			}

			fmt.Printf("[2/3] waiting for login (timeout %ds)...\n", timeout)
			token, err := waitLogin(ctx, client, session.State, time.Duration(interval)*time.Second)
			if err != nil {
				return fmt.Errorf("login wait failed: %w", err)
			}
			fmt.Printf("got accessToken=%s refreshToken=%s\n", service.MaskToken(token.AccessToken), service.MaskToken(token.RefreshToken))

			if outPath != "" {
				raw, _ := json.MarshalIndent(map[string]any{
					"accessToken":      token.AccessToken,
					"refreshToken":     token.RefreshToken,
					"expiresIn":        token.ExpiresIn,
					"refreshExpiresIn": token.RefreshExpiresIn,
				}, "", "  ")
				if err := os.WriteFile(outPath, raw, 0o600); err != nil {
					return fmt.Errorf("write %s: %w", outPath, err)
				}
				fmt.Printf("wrote %s\n", outPath)
			}

			if noSave {
				fmt.Println("[3/3] skip database (--no-save)")
				return nil
			}

			acc := &model.Account{
				Name:         name,
				JWT:          token.AccessToken,
				RefreshToken: token.RefreshToken,
				Status:       model.AccountStatusEnabled,
				Weight:       1,
			}
			saved, created, err := service.UpsertAccount(acc)
			if err != nil {
				return err
			}
			action := "updated"
			if created {
				action = "created"
			}
			fmt.Printf("[3/3] %s account id=%d name=%s username=%s\n", action, saved.ID, saved.Name, saved.Username)
			return nil
		},
	}
	cmd.Flags().StringVar(&platform, "platform", "desktop", "login platform: desktop or CLI")
	cmd.Flags().IntVar(&timeout, "timeout", 300, "seconds to wait for browser login")
	cmd.Flags().IntVar(&interval, "interval", 2, "poll interval in seconds")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the URL only, do not open a browser")
	cmd.Flags().BoolVar(&noSave, "no-save", false, "do not write the local database")
	cmd.Flags().StringVar(&outPath, "out", "", "also write tokens JSON to this file")
	cmd.Flags().StringVar(&name, "name", "", "account name override")
	return cmd
}

func waitLogin(ctx context.Context, client *service.UpstreamClient, state string, interval time.Duration) (*service.DeviceAuthToken, error) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	start := time.Now()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		pending, token, err := client.PollDeviceAuth(ctx, state)
		if err != nil {
			return nil, err
		}
		if !pending {
			fmt.Println()
			return token, nil
		}
		fmt.Fprintf(os.Stderr, "\rwaiting for login... %ds", int(time.Since(start).Seconds()))
		select {
		case <-ctx.Done():
			fmt.Println()
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func openBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", rawURL)
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		return fmt.Errorf("unsupported os %s", runtime.GOOS)
	}
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}
