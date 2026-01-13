# Load Test Simulator

A Go-based load testing tool that simulates Google Cloud resources and pushes custom metrics to Google Cloud Monitoring. This tool creates mock tenant projects, storage pools, SVMs, volumes, and replications, then continuously sends metrics data to test monitoring systems.

## Prerequisites

- Go 1.24.1 or higher
- PostgreSQL database (running on port 5433)
- Google Cloud Platform account with appropriate permissions
- Google Cloud SDK installed and authenticated
- Access to Google Cloud Billing API
- Access to Google Cloud Monitoring API
- Access to Google Cloud Resource Manager API

## Environment Setup

1. **Install Go**: Ensure you have Go 1.24.1+ installed
   ```bash
   go version
   ```

2. **Set up PostgreSQL**: The application expects a PostgreSQL database with the following connection details:
   - Host: `localhost`
   - Port: `5433`
   - User: `postgres`
   - Password: `testpass`
   - Database: `vcp`

3. **Google Cloud Authentication**: Authenticate with Google Cloud
   ```bash
   gcloud auth application-default login
   ```

4. **Set Environment Variables** (optional):
   ```bash
   export REGION=us-central1  # Default region if not set
   ```

## Installation

1. Clone the repository:
   ```bash
   cd /Users/nsingla/GolandProjects/load-script
   ```

2. Install dependencies:
   ```bash
   go mod download
   ```

## Usage

### Basic Command

```bash
go run . [flags]
```

### Command-Line Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-consumer-project` | string | `vsa-billing-09` | Consumer project ID |
| `-parent-folder` | string | `1025659400543` | Parent Folder ID for Tenant Projects |
| `-num-tps` | int | `1` | Number of Tenant Projects to create |
| `-num-pools` | int | `2` | Number of pools to create in each Tenant Project |
| `-num-volumes` | int | `3` | Number of volumes per pool |
| `-skip-migrations` | bool | `false` | Skip running DB migrations pre-flight |

### Example Commands

1. **Run with default settings**:
   ```bash
   go run .
   ```

2. **Create 3 tenant projects with 5 pools each, 10 volumes per pool**:
   ```bash
   go run . -num-tps 3 -num-pools 5 -num-volumes 10
   ```

3. **Use a specific consumer project and parent folder**:
   ```bash
   go run . -consumer-project my-project-id -parent-folder 1234567890
   ```

4. **Skip database migrations**:
   ```bash
   go run . -skip-migrations
   ```

## What the Application Does

1. **Validates Configuration**: Parses command-line flags and validates parameters
2. **Database Migrations** (optional): Runs database migrations to ensure schema is up-to-date
3. **Resource Discovery**: Searches for active tenant projects with billing enabled in the specified parent folder
4. **Mock Data Generation**: Creates mock resources in the database:
   - Accounts
   - Storage Pools
   - SVMs (Storage Virtual Machines)
   - Volumes
   - Volume Replications
5. **Metrics Generation**: Continuously sends the following metrics to Google Cloud Monitoring:
   - `volume_space_logical_used`: Logical space used by volumes
   - `volume_capacity`: Total volume capacity
   - `snapmirror_total_transfer_bytes`: Total replication transfer bytes

## Stopping the Application

The application runs continuously until interrupted. To stop it gracefully:

- Press `Ctrl+C` (SIGINT) or send a SIGTERM signal

The application will:
1. Log "Interrupt received, cleaning up created resources..."
2. Delete all created mock resources from the database
3. Close the monitoring client
4. Exit cleanly

## Project Structure

```
.
├── main.go              # Application entry point, CLI setup, signal handling
├── db_mocks.go          # Database mock data creation and cleanup functions
├── getTPs.go            # Tenant project discovery and billing validation
├── google_push.go       # Google Cloud Monitoring metrics push logic
├── go.mod               # Go module dependencies
├── go.sum               # Dependency checksums
└── README.md            # This file
```

## Database Schema

The application expects the following tables in the PostgreSQL database:
- `accounts`
- `pools`
- `svms` (Storage Virtual Machines)
- `volumes`
- `volume_replications`

Database migrations are handled by the parent repository's migrate tool (if `-skip-migrations` is not set).

## Troubleshooting

### Database Connection Issues
- Ensure PostgreSQL is running on port 5433
- Verify credentials: `postgres` / `testpass`
- Check database exists: `vcp`

### Google Cloud Authentication Issues
- Run: `gcloud auth application-default login`
- Verify project access: `gcloud projects list`
- Check API enablement:
  ```bash
  gcloud services list --enabled
  ```

### No Active Tenant Projects Found
- Verify the parent folder ID is correct
- Ensure billing is enabled on tenant projects
- Check your permissions to access the folder

### Migration Errors
- If using `-skip-migrations=false`, ensure you're running from the correct directory
- The migrate tool expects to run from the repository root

## Development

### Build the Application
```bash
go build -o load-test-simulator
```

### Run the Binary
```bash
./load-test-simulator [flags]
```

## License

[Add your license information here]
