package ui

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/sso"
	"github.com/charmbracelet/huh"
)

// Prompt handles user interactions
type Prompt struct{}

// NewPrompt creates a new prompt handler
func NewPrompt() *Prompt {
	return &Prompt{}
}

// Select prompts the user to select from a list of items
func (p *Prompt) Select(label string, items []string) (string, error) {
	var selected string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title(label).
				Options(huh.NewOptions(items...)...).
				Value(&selected),
		),
	)

	if err := form.Run(); err != nil {
		return "", fmt.Errorf("select failed: %w", err)
	}
	return selected, nil
}

// Input prompts the user for input
func (p *Prompt) Input(label string, validate func(string) error, defaultValue ...string) (string, error) {
	var result string
	
	// Set default value if provided
	if len(defaultValue) > 0 && defaultValue[0] != "" {
		result = defaultValue[0]
	}
	
	input := huh.NewInput().
		Title(label).
		Validate(func(s string) error {
			if validate != nil {
				return validate(s)
			}
			return nil
		}).
		Value(&result)

	form := huh.NewForm(
		huh.NewGroup(input),
	)

	if err := form.Run(); err != nil {
		return "", fmt.Errorf("input failed: %w", err)
	}
	return result, nil
}

// SelectAccount prompts the user to select an AWS account
func (p *Prompt) SelectAccount(accounts *sso.ListAccountsOutput) (string, string, error) {
	accountMap := make(map[string]string)
	accountNames := make([]string, 0, len(accounts.AccountList))

	for _, acc := range accounts.AccountList {
		display := fmt.Sprintf("%s (%s)", *acc.AccountName, *acc.AccountId)
		accountNames = append(accountNames, display)
		accountMap[display] = *acc.AccountId
	}

	selected, err := p.Select("Select an AWS account", accountNames)
	if err != nil {
		return "", "", err
	}

	return selected, accountMap[selected], nil
}

// SelectRole prompts the user to select a role
func (p *Prompt) SelectRole(roles *sso.ListAccountRolesOutput) (string, error) {
	roleNames := make([]string, 0, len(roles.RoleList))
	for _, role := range roles.RoleList {
		roleNames = append(roleNames, *role.RoleName)
	}
	return p.Select("Select a role", roleNames)
}

// Confirm prompts the user for a yes/no confirmation
func (p *Prompt) Confirm(label string) (bool, error) {
	var confirm bool
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(label).
				Affirmative("Yes!").
				Negative("No.").
				Value(&confirm),
		),
	)

	if err := form.Run(); err != nil {
		return false, fmt.Errorf("confirmation failed: %w", err)
	}
	return confirm, nil
}
