<div align="center">
<h1>🌈 Bifrost </h1>
A command-line utility to simplify connecting to AWS RDS/Redis instances through bastion hosts utilising AWS SSM Session Manager.
</div>

## Features

- **Auto-Discovery**: Discovers all RDS and Redis resources across your AWS accounts in parallel
- **IAM Database Auth**: Generates IAM auth tokens for RDS clusters with one-click `psql` command
- **Admin Escalation**: Connect as `su-` superuser for database admin tasks
- **SSO Integration**: Uses the Lottie SSO portal automatically — no configuration needed
- **Connection Profiles**: Save and reuse connection configurations for resources discovery doesn't cover
- **Keep Alive**: Maintains stable connections with periodic health checks
- **Smart Bastion Selection**: Auto-discovers and selects SSM-managed bastion hosts

## Installation

### Using Homebrew

Bifrost is distributed via a private Homebrew tap. To install with SSH run:
```bash
brew tap LottieHQ/tap git@github.com:LottieHQ/homebrew-tap.git
brew install lottiehq/tap/bifrost
```

## Quick Start

No configuration needed. Just run:

```bash
bifrost connect
```

Bifrost will:
1. Authenticate via SSO (opens browser on first run, then caches the token)
2. Discover all RDS clusters and Redis instances across your accounts
3. Present a menu to select a resource
4. Auto-discover and select a bastion host
5. For IAM-enabled databases: prompt for auth method and generate a token
6. Start the SSM tunnel and copy the `psql` command to your clipboard

### IAM Authentication

Databases tagged with `bifrost:iam-auth` offer three auth methods:

- **IAM** — connects as your SSO identity (e.g. `dan.williams@lottie.org`)
- **IAM Admin** — connects as superuser (e.g. `su-dan.williams@lottie.org`). Requires membership of `infra-aws-database-admins-stg` (kubernetes_non_prod) or `infra-aws-database-admins-prd` (kubernetes_prod), granted for 1 hour by the Ravenna "Database superuser access" request. No other account has `su-` users. Bifrost prints this requirement, or a not-provisioned warning on other accounts, and generates the token without asking for confirmation.
- **Password** — manual password entry

### Other Options

```bash
# Use a saved connection profile
bifrost connect --profile staging-db

# Force re-discovery (ignores 1-hour cache)
bifrost connect --refresh

# Manual setup (bypass discovery)
bifrost connect --account-id 904233092296 --service rds

# Custom keep alive interval
bifrost connect --keep-alive-interval 60s
```

### Managing Profiles

Saved profiles are useful for resources that discovery doesn't cover, or for pinning specific connection settings.

```bash
# Create a connection profile
bifrost profile create --name staging-db --service rds

# List profiles
bifrost profile list
```

## How It Works

**Discovery**: On first run, Bifrost scans all AWS accounts you have access to (via SSO) and discovers RDS clusters, Redis replication groups, and SSM-managed bastion hosts. Results are cached for 1 hour at `~/.bifrost/discovery-cache-*.json`. Use `--refresh` to force a re-scan.

**IAM Auth**: For RDS clusters tagged with `bifrost:iam-auth=true`, Bifrost generates a 15-minute IAM auth token using your SSO credentials. The full `psql` connection command is copied to your clipboard.

**Keep Alive**: Bifrost sends periodic TCP health checks (every 30 seconds by default) to prevent SSM session timeouts.

**Bastion Selection**: Bifrost only considers SSM-managed instances in the target resource's VPC. If any of them are tagged `bifrost:bastion=true`, only the tagged ones are candidates, so other SSM-managed hosts in the VPC (such as the Datastream relays) are skipped. A single candidate is auto-selected; with several, you're prompted to choose.
## Updating
### Using Homebrew

To update bifrost to the latest version:

1. Update Homebrew's formulae:
   ```bash
   brew update
   ```

2. Upgrade bifrost:
   ```bash
   brew upgrade bifrost
   ```


## Upcoming features

- [ ] Multiple simultaneous connections
- [ ] Token refresh without reconnecting

## Developing
### Requirements

- Go 1.25+
- AWS SSM Session Manager Plugin [brew install --cask session-manager-plugin](https://formulae.brew.sh/cask/session-manager-plugin#default) or [official docs](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html)


## Contributing

Still working out a few things, so unless for fixing a straightforward bug or updating docs, please drop me a message or open an issue before opening a PR. Thank you! 🙏🏻

## License

MIT - see [LICENSE.md](LICENSE.md)

## Acknowledgements

Inspiration taken from `aws-sso-utils` and `common-fate/granted`.

Special thanks to @diosdavid for the initial idea.
