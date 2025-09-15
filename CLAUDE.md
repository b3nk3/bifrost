# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Development Commands

```bash
# Build the application
go build -o bifrost

# Run the application directly
go run main.go [command]

# Run with live reload during development
go run main.go connect --help

# Install dependencies
go mod tidy

# Test the application
go build -v && ./bifrost --help
```

## Architecture Overview

Bifrost is a Go CLI application that simplifies connecting to AWS RDS/Redis instances through bastion hosts using AWS SSM Session Manager. The application uses a **two-level profile system** and **hierarchical configuration** as its core architectural pattern.

### Key Architectural Concepts

#### Dual Profile System
- **SSO Profiles**: Handle authentication (SSO URL, region) - stored globally
- **Connection Profiles**: Complete connection configs that reference SSO profiles - can be local or global

#### Configuration Hierarchy
- **Global Config** (`~/.bifrost/config.yaml`): SSO profiles + global connection profiles  
- **Local Config** (`.bifrost.config.yaml`): Project-specific connection profiles
- **Priority**: Local connection profiles override global ones with same name

#### Authentication Flow
1. SSO profiles contain authentication details (StartURL, SSORegion)
2. Token caching in `~/.aws/sso/cache/` (AWS CLI compatible format)
3. Device flow authentication with browser-based approval
4. Automatic SSO region detection from Content-Security-Policy headers via HTTP HEAD request
5. Smart profile defaults: Auto-selects when only one SSO profile exists

#### Keep Alive System
1. **Enabled by default** with `--keep-alive` flag (configurable interval via `--keep-alive-interval`)
2. **SSM tunnel maintenance**: Keep alive performs simple TCP connection checks to maintain the SSM tunnel
3. **Works with all services**: Supports both RDS and Redis connections
4. **Non-blocking operation**: Runs in background goroutine, doesn't interfere with connection
5. **Graceful error handling**: Keep alive failures are logged but don't terminate the connection

## Core Components

### Command Structure
- `cmd/auth.go` - SSO profile management (`configure`, `login`, `list`, `logout`)
- `cmd/connect.go` - Main connection logic with resource discovery
- `cmd/profile.go` - Connection profile management (`create`, `list`, `delete`)
- `cmd/root.go` - Root Cobra command setup

### Configuration Management (`internal/config/`)
The config system merges global and local configurations:

```go
// Two-level structure
type SSOProfile struct {
    StartURL  string `yaml:"sso_url"`
    SSORegion string `yaml:"sso_region"`
}

type ConnectionProfile struct {
    SSOProfile        string `yaml:"sso_profile"`         // References SSO profile
    AccountID         string `yaml:"account_id"`
    RoleName          string `yaml:"role_name"`
    Region            string `yaml:"region"`
    Environment       string `yaml:"environment"`
    ServiceType       string `yaml:"service"`
    Port              string `yaml:"port"`
    BastionInstanceID string `yaml:"bastion_instance_id"` // Direct bastion instance ID
    RDSInstanceName   string `yaml:"rds_instance_name"`   // Specific RDS instance name
    RedisClusterName  string `yaml:"redis_cluster_name"`  // Specific Redis cluster name
}
```

### Authentication System (`internal/sso/`)
- `client.go` - SSO OIDC device flow implementation
- `cache.go` - Token caching compatible with AWS CLI
- `sso-region-helper.go` - Auto-detection of SSO region from start URL

## Important Development Patterns

### Configuration Loading Priority
1. Load global SSO profiles and connection profiles from `~/.bifrost/config.yaml`
2. Load local connection profiles from `.bifrost.config.yaml` (if exists)
3. Local connection profiles override global ones by name
4. SSO profiles are always global (never local)

### Enhanced Connect Command Flow
The connect command (`cmd/connect.go`) implements a sophisticated user experience:

1. **Profile Resolution**: Check `--profile` flag → Interactive profile selection → Manual setup
2. **Visual Profile Selection**: 
   - `🔗 profile-name` for existing connection profiles
   - `⚙️ Manual setup` for interactive configuration
3. **Smart Defaults**: Profile values serve as defaults, prompting only for missing values
4. **Profile Saving Offer**: After manual setup (before SSM connection), offer to save configuration:
   - Suggests `{environment}-{service}` naming (e.g., "dev-rds")
   - Choice between `📁 Local (.bifrost.config.yaml)` or `🌍 Global (~/.bifrost/config.yaml)`
5. **SSO Authentication**: Auto-detect single SSO profile or prompt for selection
6. **Resource Discovery**: Tag-based filtering with interactive selection for multiple matches, or direct instance/cluster specification
7. **Bastion Instance ID Handling**: Prompt for bastion instance ID if not provided in profile or flags
8. **Keep Alive Setup**: Configure and start background keep alive if enabled
9. **Enhanced Signal Handling**: Graceful shutdown with Ctrl+C, proper cleanup of SSM sessions

### Resource Discovery Pattern
All AWS resources (bastion hosts, RDS, Redis) can be discovered using:
- **Tag filtering**: `env=<environment>` tag matching (automatic discovery)
- **Direct specification**: Use specific instance IDs or cluster names in profiles
- **Interactive selection**: Single resource confirmed, multiple resources prompted
- **Service patterns**:
  - Bastion: EC2 instances with `Name=*bastion*` + `env=<environment>`, or direct instance ID
  - RDS: DB instances with `env=<environment>` tag, or specific instance name
  - Redis: ElastiCache clusters with `env=<environment>` tag, or specific cluster name

### Keep Alive Architecture
The keep alive system maintains active SSM tunnels to prevent timeouts:

**Core Functions:**
- `startSSMPortForwardingWithKeepAlive()` - Enhanced SSM session with keep alive support
- `startKeepAlive()` - Background goroutine that manages periodic keep alive checks
- `performKeepAlive()` - Simple TCP connection check to maintain SSM tunnel

**Architecture Pattern:**
```go
Main Goroutine (SSM Session)
├── SSM Port Forwarding Process
├── Signal Handler (Ctrl+C)
└── Keep Alive Goroutine
    ├── Timer Ticker (configurable interval)
    └── TCP Connection Test → localhost:port
```

**Key Design Decisions:**
- Keep alive runs in separate goroutine to avoid blocking SSM session
- Failures are logged but don't terminate the connection (graceful degradation)
- Simple TCP connection check works for both RDS and Redis
- Context cancellation ensures clean shutdown when connection ends

### Viper Configuration Gotchas
The config system uses both `yaml` and `mapstructure` tags because:
- `yaml` tags control field names when writing YAML files
- `mapstructure` tags control field names when reading YAML files
- Both are needed to ensure consistent field naming in config files

Example:
```go
type ConnectionProfile struct {
    Region string `yaml:"region" mapstructure:"region"`
    // Without both tags, Viper would write "region" but read "Region"
}
```

## Testing and Validation

### Manual Testing Flow
```bash
# Test SSO configuration with auto-region detection
./bifrost auth configure --profile test-profile

# Test SSO authentication  
./bifrost auth login --profile test-profile

# Test connection profiles (defaults to local storage)
./bifrost profile create --name test-conn --env dev --service rds --bastion-id i-1234567890abcdef0

# Test enhanced connect with profile selection
./bifrost connect
# Will show: ⚙️ Manual setup, 🔗 test-conn

# Test manual setup with profile saving offer
./bifrost connect
# Select "⚙️ Manual setup" → configure → offered to save as profile

# Test keep alive functionality (works with all services)
./bifrost connect --profile dev-redis --keep-alive-interval 15s
# Should see: 💓 Keep alive enabled (interval: 15s)
# Connection will perform TCP checks every 15 seconds

# Test keep alive with RDS
./bifrost connect --profile dev-rds --keep-alive-interval 10s
# Should see: 💓 Keep alive enabled (interval: 10s)
# Connection will perform TCP checks every 10 seconds

# Test without keep alive
./bifrost connect --profile dev-redis --keep-alive=false
# No keep alive messages should appear
```

### Connection Profile Structure in Local Config
```yaml
# .bifrost.config.yaml
connection_profiles:
  dev-rds:
    sso_profile: work         # References global SSO profile
    account_id: "123456789"
    role_name: PowerUserAccess
    region: us-west-2
    environment: dev
    service: rds
    port: "3306"
    bastion_instance_id: "i-1234567890abcdef0"  # Optional: Direct bastion instance
    rds_instance_name: "dev-database"           # Optional: Specific RDS instance
    redis_cluster_name: "dev-cache"             # Optional: Specific Redis cluster
```

## Key Dependencies

- **Cobra**: CLI framework and command routing
- **Viper**: Configuration management with YAML support
- **Huh (Charm)**: Interactive prompts and forms
- **AWS SDK v2**: AWS service integration (SSO, EC2, RDS, ElastiCache)
- **Browser**: Opens SSO authentication URLs
- **Redis Client (go-redis/redis/v8)**: Redis PING commands for keep alive
- **MySQL Driver (go-sql-driver/mysql)**: MySQL SELECT 1 queries for RDS keep alive
- **PostgreSQL Driver (lib/pq)**: PostgreSQL SELECT 1 queries for RDS keep alive

## Distribution

- Built with **GoReleaser** (`.goreleaser.yaml`)
- Distributed via private Homebrew tap (`b3nk3/homebrew-tap`)
- Dependencies: `awscli`, `gh`, `session-manager-plugin` (cask)
- Currently macOS only (Darwin)

## Critical Implementation Details

### Profile Saving Timing
The profile saving offer (`offerToSaveProfile()` in `cmd/connect.go`) executes **before** starting the SSM session because:
- SSM port forwarding blocks with `cmd.Run()` until Ctrl+C
- Users lose the opportunity to save if the offer comes after
- Solution: Offer saving after parameter gathering but before connection

### Visual UX Patterns
Consistent emoji usage throughout the application:
- `🔐` SSO profiles and authentication
- `🔗` Connection profiles  
- `⚙️` Manual setup option
- `📁` Local storage, `🌍` Global storage
- `✅` Success, `❌` Error, `⚠️` Warning, `💡` Tips

### Profile Storage Strategy
- **Connection profiles default to LOCAL** (`.bifrost.config.yaml`)
- Use `--global` flag for system-wide profiles
- **SSO profiles are ALWAYS global** (authentication should be user-wide)
- Local profiles override global ones with same name during config loading

## Common Development Scenarios

When adding new AWS services, follow the resource discovery pattern:
1. Add service type to connection profile enum
2. Implement tag-based filtering in connect command  
3. Add interactive selection for multiple resources
4. Follow existing patterns in `getRDSEndpoint()` and `getRedisEndpoint()`

When modifying configuration structure:
1. Update both `yaml` and `mapstructure` tags
2. Consider impact on existing config files
3. Test global vs local configuration merging
4. Handle profile saving offer integration

When enhancing the connect command:
1. Maintain the profile selection → manual setup → save offer flow
2. Test visual distinctions work correctly
3. Ensure profile saving happens before SSM session starts
4. Validate auto-detection of SSO profiles works