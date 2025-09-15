# Bifrost

A command-line utility to simplify connecting to AWS RDS/Redis instances through bastion hosts utilising AWS SSM Session Manager.

## Features

- **Smart Connection Management**: Automatic discovery of RDS/Redis instances and bastion hosts via tags
- **SSO Integration**: Seamless AWS SSO authentication with token caching
- **Connection Profiles**: Save and reuse connection configurations (local or global)
- **Keep Alive**: Maintains stable connections with periodic health checks (like TablePlus)
- **Interactive Experience**: Intuitive prompts with smart defaults and profile selection

## Installation

### Prerequisites

To install from our private Homebrew tap, a GitHub PAT (Personal Access Token) is required.
You can generate a long lived one, but my recommended method is to use the GitHub CLI and generate a short lived token on install. (this will have to be repeated when updating `bifrost`) 

- AWS CLI [brew docs](https://formulae.brew.sh/formula/awscli) or [official docs](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html)
  ```bash
  brew install awscli
  ```

- AWS CLI SSM plugin [brew docs](https://formulae.brew.sh/cask/session-manager-plugin#default) or [official docs](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html)
  ```bash
  brew install --cask session-manager-plugin
  ```
  
- GitHub CLI (`gh`) must be installed and authenticated. You can install it with:
  ```bash
  brew install gh
  ```
  Then authenticate with:
  ```bash
  gh auth login
  ```

### Using Homebrew

1. First, export your GitHub token to allow access to private repositories:
   ```bash
   export HOMEBREW_GITHUB_API_TOKEN="$(gh auth token)"
   ```

2. Add the Homebrew tap (using the SSH GitHub URL):
   ```bash
   brew tap b3nk3/tap git@github.com:b3nk3/homebrew-tap.git
   ```

3. Install bifrost:
   ```bash
   brew install bifrost
   ```

## Quick Start

### 1. Configure SSO Profile
```bash
# Set up your AWS SSO profile
bifrost auth configure --profile work

# Login with SSO
bifrost auth login --profile work
```

### 2. Connect to Database
```bash
# Interactive mode (recommended for first time)
bifrost connect

# Using a saved profile
bifrost connect --profile dev-rds

# Direct connection with flags
bifrost connect --env dev --service rds --port 3306

# With custom keep alive interval
bifrost connect --profile dev-redis --keep-alive-interval 60s

# Disable keep alive
bifrost connect --profile dev-rds --keep-alive=false
```

### 3. Manage Profiles
```bash
# Create a connection profile
bifrost profile create --name staging-db --env stg --service rds

# List profiles
bifrost profile list

# Help
bifrost help
```

## How It Works

**Keep Alive**: Bifrost automatically sends lightweight health checks to your database connections (Redis `PING`, RDS `SELECT 1`) every 30 seconds by default. This prevents timeout disconnections, similar to how TablePlus maintains stable connections.

**Smart Discovery**: Resources are discovered via AWS tags (`env=<environment>`). Bastion hosts are found by name pattern (`*bastion*`) + environment tag.

**Profile System**: Save connection settings locally (`.bifrost.config.yaml`) or globally (`~/.bifrost/config.yaml`). SSO profiles are always global, connection profiles can be either.
## Updating
### Using Homebrew

To update bifrost to the latest version:

1. First, ensure your GitHub token is set:
   ```bash
   export HOMEBREW_GITHUB_API_TOKEN="$(gh auth token)"
   ```

2. Update Homebrew's formulae:
   ```bash
   brew update
   ```

3. Upgrade bifrost:
   ```bash
   brew upgrade bifrost
   ```


## Upcoming Features

- [ ] Customizable tag filtering (for open source compatibility)
- [ ] Multiple simultaneous connections
- [ ] Terminal UI (TUI) interface

## Developing
### Requirements

- Go 1.24
- AWS CLI [brew install awscli](https://formulae.brew.sh/formula/awscli) or [official docs](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html)
- AWS CLI SSM plugin [brew install --cask session-manager-plugin](https://formulae.brew.sh/cask/session-manager-plugin#default) or [official docs](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html)


## Contributing

Still working out a few things, so unless for fixing a straightforward bug or updating docs, please drop me a message or open an issue before opening a PR. Thank you! 🙏🏻

## License

MIT - see [LICENSE.md](LICENSE.md)

## Acknowledgements

Inspiration taken from `aws-sso-utils` and `common-fate/granted`.