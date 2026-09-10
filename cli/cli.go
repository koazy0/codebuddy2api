package cli

import (
	"codebuddy-gateway/cli/command"

	"github.com/spf13/cobra"
)

const (
	cliName        = "codebuddy-gateway"
	cliDescription = "Lightweight CodeBuddy OpenAI-compatible gateway"
)

var (
	rootCmd = &cobra.Command{
		Use:   cliName,
		Short: cliDescription,
	}
)

func init() {
	rootCmd.PersistentFlags().StringP("config", "c", "config.yaml", "config file path")
	rootCmd.PersistentFlags().String("api-key", "", "downstream OpenAI API key, overrides config and env")
	rootCmd.PersistentFlags().String("admin-key", "", "admin API key, overrides config and env")
	rootCmd.AddCommand(command.NewServerCommand())
}

func Execute() {
	cobra.CheckErr(rootCmd.Execute())
}
