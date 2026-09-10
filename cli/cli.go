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
	rootCmd.AddCommand(command.NewServerCommand())
}

func Execute() {
	cobra.CheckErr(rootCmd.Execute())
}
