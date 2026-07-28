#!/bin/bash

# This script connects a VSIM to VCP by creating database entries

set -euo pipefail

# Default values
ACCOUNT_ID=1
PROJECT_ID=""
POOL_NAME="vcp-vsim-pool"
SVM_NAME="vcp-vsim-svm"
POSTGRES_URL=""
POSTGRES_USER="postgres"
POSTGRES_PASS="testpass"
POSTGRES_SSL_MODE="disable"
QOS_TYPE="auto"
TOTAL_THROUGHPUT_MIBPS=1120
TOTAL_IOPS=2400
VLM_CONFIG_JSON=""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# =============================================================================
# USAGE AND ARGUMENT PARSING
# =============================================================================

# Function to show usage
usage() {
    cat << EOF
Usage: $0 --vsim-config <config_file> [--vsim-config <config_file> ...] --project-number <project_id> --region <region>

Connect a VSIM to VCP by creating database entries.

Required arguments:
  --vsim-config     Path to a VSIM configuration file (specify at least one, can be used multiple times). There will be a config file for each node in the VSIM cluster.
  --project-number  GCP project number/ID to use
  --region          AWS/GCP region (e.g., us-east1, us-west-2)

Optional arguments:
  --debug           Enable debug logging
  --postgres-url    PostgreSQL connection URL (if not provided, will get from k8s cluster)
  --postgres-user   PostgreSQL username (default: postgres)
  --postgres-pass   PostgreSQL password (default: testpass)
  --postgres-ssl-mode PostgreSQL SSL mode (default: disable, options: disable, require, verify-ca, verify-full)
  --qos-type        QoS type (default: auto, options: auto, manual)
  --help            Show this help message

Environment variables:
  ONTAP_PASSWORD    Password for ONTAP cluster admin user (required)
  DEBUG             Enable debug logging (true/false)

Examples:
  $0 --vsim-config /path/to/vsim1.conf --vsim-config /path/to/vsim2.conf --project-number 261841488504 --region us-east1
  $0 --vsim-config /path/to/vsim1.conf --vsim-config /path/to/vsim2.conf --vsim-config /path/to/vsim3.conf --project-number 261841488504 --region us-west-2 --postgres-url localhost:5432 --postgres-user admin --postgres-pass mypass --postgres-ssl-mode require
  DEBUG=true $0 --vsim-config /path/to/vsim1.conf --vsim-config /path/to/vsim2.conf --project-number 261841488504 --region us-west-2
EOF
}

# Parse command line arguments
parse_args() {
    VSIM_CONFIGS=()
    while [[ $# -gt 0 ]]; do
        case $1 in
            --vsim-config)
                VSIM_CONFIGS+=("$2")
                shift 2
                ;;
            --project-number)
                PROJECT_ID="$2"
                shift 2
                ;;
            --region)
                REGION="$2"
                shift 2
                ;;
            --postgres-url)
                POSTGRES_URL="$2"
                shift 2
                ;;
            --postgres-user)
                POSTGRES_USER="$2"
                shift 2
                ;;
            --postgres-pass)
                POSTGRES_PASS="$2"
                shift 2
                ;;
            --postgres-ssl-mode)
                POSTGRES_SSL_MODE="$2"
                shift 2
                ;;
            --qos-type)
                QOS_TYPE="$2"
                shift 2
                ;;
            --debug)
                DEBUG=true
                shift
                ;;
            --help)
                usage
                exit 0
                ;;
            *)
                log_error "Unknown argument: $1"
                usage
                exit 1
                ;;
        esac
    done

    # Validate required arguments
    if [[ ${#VSIM_CONFIGS[@]} -lt 1 ]]; then
        log_error "You must provide at least one VSIM configuration file with the --vsim-config flag"
        usage
        exit 1
    fi

    if [[ -z "${PROJECT_ID:-}" ]]; then
        log_error "You must provide a project number with the --project-number flag"
        usage
        exit 1
    fi

    if [[ -z "${REGION:-}" ]]; then
        log_error "You must provide a region with the --region flag"
        usage
        exit 1
    fi

    for config in "${VSIM_CONFIGS[@]}"; do
        if [[ ! -f "$config" ]]; then
            log_error "VSIM configuration file not found: $config"
            exit 1
        fi
    done

     # Validate qos-type if provided
    if [[ "$QOS_TYPE" != "auto" && "$QOS_TYPE" != "manual" ]]; then
        log_error "Invalid qos-type: $QOS_TYPE. Must be 'auto' or 'manual'"
        usage
        exit 1
    fi
}

# =============================================================================
# HELPER FUNCTIONS
# =============================================================================

# Logging functions
log_info() {
    echo -e "${GREEN}[INFO]${NC} $1" >&2
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1" >&2
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1" >&2
}

log_debug() {
    if [[ "${DEBUG:-false}" == "true" ]]; then
        echo -e "[DEBUG] $1" >&2
    fi
}

# Escape single quotes for SQL strings
escape_sql() {
    printf "%s" "$1" | sed "s/'/''/g"
}

# Build a minimal VLM config with at least one HA pair
build_vlm_config() {
    local region="$1"
    local deployment_name="$2"
    local zone1="${region}-a"
    local zone2="${region}-b"
    local vm1_name="${VSIM1_hostname:-vsim-node-1}"
    local vm2_name="${VSIM2_hostname:-vsim-node-2}"
    local vm1_mgmt="${VSIM1_CLUSTER_MGMT_IP:-}"
    local vm2_mgmt="${VSIM2_CLUSTER_MGMT_IP:-}"
    local ilb_ip="${VSIM1_DATA_IP1:-}"

    if [[ -z "$ilb_ip" ]]; then
        log_warn "VSIM1_DATA_IP1 is empty; ilbnas LIF IP will be blank in VLM config"
    fi

    VLM_CONFIG_JSON=$(jq -c -n \
        --arg region "$region" \
        --arg zone1 "$zone1" \
        --arg zone2 "$zone2" \
        --arg deployment_id "$deployment_name" \
        --arg svm_name "$SVM_NAME" \
        --arg ilb_ip "$ilb_ip" \
        --arg vm1_name "$vm1_name" \
        --arg vm2_name "$vm2_name" \
        --arg vm1_mgmt "$vm1_mgmt" \
        --arg vm2_mgmt "$vm2_mgmt" \
        '{
            cloud: {
                ha_pair: [
                    {
                        vm1: {
                            region: $region,
                            zone: $zone1,
                            name: $vm1_name,
                            host_name: $vm1_name,
                            node_index: 0,
                            is_mediator: false,
                            vsa_management_ip: $vm1_mgmt
                        },
                        vm2: {
                            region: $region,
                            zone: $zone2,
                            name: $vm2_name,
                            host_name: $vm2_name,
                            node_index: 1,
                            is_mediator: false,
                            vsa_management_ip: $vm2_mgmt
                        },
                        mediator: {
                            is_mediator: true
                        }
                    }
                ]
            },
            deployment: {
                provider: "gcp",
                deployment_id: $deployment_id,
                region: $region,
                zone: {zone1: $zone1, zone2: $zone2, mediator_zone: ""},
                deployment_type: "non_shared_ha",
                num_ha_pair: 1,
                dev_flags: {enable_ilb_support: false}
            },
            svm: {
                ($svm_name): {
                    svm_name: $svm_name,
                    svm_uuid: "",
                    svm_lifs: {
                        ilbnas: [
                            {
                                lif_name: "ilbnas0",
                                vsa_ip_type: "ilbnas",
                                ip: $ilb_ip,
                                region: $region,
                                home_node: "node0",
                                probe_port: 0,
                                network_config: {
                                    subnet: "",
                                    vpc: "",
                                    gateway: "",
                                    gcp_network_config: {subnet_project_id: ""}
                                }
                            }
                        ]
                    }
                }
            }
        }')
}

# Check if required tools are available
check_dependencies() {
    local deps=("kubectl" "psql" "jq" "curl")
    local missing=()

    for dep in "${deps[@]}"; do
        if ! command -v "$dep" &> /dev/null; then
            missing+=("$dep")
        fi
    done

    if [[ ${#missing[@]} -gt 0 ]]; then
        log_error "Missing required dependencies: ${missing[*]}"
        log_error "Please install the missing tools and try again"
        exit 1
    fi
}

# Extract VSIM values from configuration file (generic function)
extract_vsim_config() {
    local config_file="$1"
    local config_name="$2"
    local var_prefix="$3"

    log_debug "Extracting VSIM values from configuration file: $config_file"

    while IFS= read -r line; do
        # Skip comments and empty lines
        if [[ "$line" =~ ^#.*$ ]] || [[ -z "${line// }" ]]; then
            continue
        fi

        # Skip net blocks and indented lines
        if [[ "$line" =~ ^net\ .*$ ]] || [[ "$line" =~ ^[[:space:]]{3}.*$ ]]; then
            continue
        fi

        # Extract key=value pairs
        if [[ "$line" =~ ^[^=]+=[^=]*$ ]]; then
            local key="${line%%=*}"
            local value="${line#*=}"
            key=$(echo "$key" | xargs)  # trim whitespace
            value=$(echo "$value" | xargs)  # trim whitespace

            # Create global variables with prefix using printf
            printf -v "${var_prefix}_${key}" '%s' "$value"
        fi
    done < "$config_file"

    log_debug "Extracted VSIM values from $config_name"
}

# Extract VSIM values from all configuration files
extract_all_vsim_values() {
    for idx in "${!VSIM_CONFIGS[@]}"; do
        local config_file="${VSIM_CONFIGS[$idx]}"
        local prefix="VSIM$((idx+1))"
        extract_vsim_config "$config_file" "Config$((idx+1))" "$prefix"
    done
}

# Generate UUID (using uuidgen or fallback method)
generate_uuid() {
    if command -v uuidgen &> /dev/null; then
        uuidgen | tr '[:upper:]' '[:lower:]'
    else
        # Fallback UUID generation
        od -x /dev/urandom | head -1 | awk '{OFS="-"; print $2$3,$4,$5,$6,$7$8$9}'
    fi
}

# Get current timestamp in PostgreSQL format
get_timestamp() {
    date '+%Y-%m-%d %H:%M:%S'
}

# Configure PostgreSQL connection
configure_postgres_connection() {
    if [[ -n "$POSTGRES_URL" ]]; then
        log_info "Using provided PostgreSQL URL: $POSTGRES_URL"
        POSTGRES_ENDPOINT="$POSTGRES_URL"
    else
        log_info "No PostgreSQL URL provided, getting from Kubernetes cluster"
        get_postgres_endpoint
    fi

    # Build connection string with provided credentials
    POSTGRES_CONNECTION_STRING="postgres://$POSTGRES_USER:$POSTGRES_PASS@$POSTGRES_ENDPOINT/vcp?sslmode=$POSTGRES_SSL_MODE"
    log_info "PostgreSQL connection configured with user: $POSTGRES_USER"
}

# Get PostgreSQL service endpoint
get_postgres_endpoint() {
    log_debug "Getting PostgreSQL service endpoint"

    # Get the postgres-nodeport service URL
    local service_info
    service_info=$(kubectl get service postgres-nodeport -n default -o json 2>/dev/null || true)

    if [[ -z "$service_info" ]]; then
        log_error "PostgreSQL service 'postgres-nodeport' not found in default namespace. Try setting --postgres-url manually."
        exit 1
    fi

    # Extract NodePort
    local node_port
    node_port=$(echo "$service_info" | jq -r '.spec.ports[0].nodePort // empty')

    if [[ -z "$node_port" ]]; then
        log_error "Could not find NodePort for postgres-nodeport service"
        exit 1
    fi

    # Get node IP (using first ready node)
    local node_ip
    node_ip=$(kubectl get nodes -o json | jq -r '.items[0].status.addresses[] | select(.type=="ExternalIP" or .type=="InternalIP") | .address' | head -n1)

    if [[ -z "$node_ip" ]]; then
        log_error "Could not determine node IP for PostgreSQL connection"
        exit 1
    fi

    POSTGRES_ENDPOINT="${node_ip}:${node_port}"
    log_info "PostgreSQL endpoint: $POSTGRES_ENDPOINT"
}

# =============================================================================
# DATABASE FUNCTIONS
# =============================================================================

# Create or verify database account exists
create_or_verify_account() {
    local project_number="$PROJECT_ID"
    local current_time
    current_time=$(get_timestamp)

    log_info "Checking if account exists for project: $project_number"

    # Check if account already exists
    local existing_account_id
    existing_account_id=$(psql "$POSTGRES_CONNECTION_STRING" -t -c "SELECT id FROM accounts WHERE name='$project_number';" | xargs)

    if [[ -n "$existing_account_id" ]]; then
        log_info "Account already exists with ID: $existing_account_id"
        ACCOUNT_ID="$existing_account_id"
        return 0
    fi

    log_info "Creating new account for project: $project_number"

    # Generate UUID for new account
    local account_uuid
    account_uuid=$(generate_uuid)

    # Insert account entry
    local account_insert_sql="
        INSERT INTO accounts (
            uuid, created_at, updated_at, name, description, state, state_details
        ) VALUES (
            '$account_uuid', '$current_time', '$current_time', '$project_number',
            'Account for project $project_number', 'READY',
            'Available for use'
        );
    "

    psql "$POSTGRES_CONNECTION_STRING" -c "$account_insert_sql"

    # Get the new account ID
    ACCOUNT_ID=$(psql "$POSTGRES_CONNECTION_STRING" -t -c "SELECT id FROM accounts WHERE uuid='$account_uuid';" | xargs)

    log_info "Created new account with ID: $ACCOUNT_ID"
}

# Create database pool
create_db_pool() {
    local region="$1"
    local current_time
    current_time=$(get_timestamp)
    local pool_uuid
    pool_uuid=$(generate_uuid)

    # Create unique deployment name using project ID and timestamp
    local unique_deployment_name="${POOL_NAME}-${PROJECT_ID}-$(date +%s)"

    log_info "Creating VCP database pool"

    local pool_attributes_json
    local vlm_config_value="NULL"
    if [[ "$QOS_TYPE" == "manual" ]]; then
        pool_attributes_json="{\"primary_zone\": \"${region}-a\", \"secondary_zone\": \"${region}-b\", \"throughput\": ${TOTAL_THROUGHPUT_MIBPS}, \"iops\": ${TOTAL_IOPS}}"
        build_vlm_config "$region" "$unique_deployment_name"
        local vlm_config_escaped
        vlm_config_escaped=$(escape_sql "$VLM_CONFIG_JSON")
        vlm_config_value="'$vlm_config_escaped'"
    else
        pool_attributes_json="{\"primary_zone\": \"${region}-a\", \"secondary_zone\": \"${region}-b\", \"throughput\": 64}"
    fi

    # Insert pool entry
    local pool_insert_sql="
        INSERT INTO pools (
            uuid, created_at, updated_at, name, state, state_details,
            service_level, size_in_bytes, vendor_id, network, deployment_name,
            pool_attributes, pool_credentials, build_info, account_id, qos_type, vlm_config
        ) VALUES (
            '$pool_uuid', '$current_time', '$current_time', '$POOL_NAME', 'READY',
            'Available for use', 'FLEX', 2199023255552,
            '/projects/$PROJECT_ID/locations/${region}-a/pools/$POOL_NAME',
            'projects/$PROJECT_ID/global/networks/cvs-test',
            '$unique_deployment_name',
            '$pool_attributes_json',
            '{\"password\": \"${ONTAP_PASSWORD}\", \"auth_type\": 0}',
            '{\"ontapVersion\": \"9.18.1\"}',
            $ACCOUNT_ID, '$QOS_TYPE', ${vlm_config_value}
        );
    "

    psql "$POSTGRES_CONNECTION_STRING" -c "$pool_insert_sql"

    # Get pool ID
    POOL_ID=$(psql "$POSTGRES_CONNECTION_STRING" -t -c "SELECT id FROM pools WHERE uuid='$pool_uuid';" | xargs)

    log_info "Created pool with ID: $POOL_ID"
}

# Create database SVM
create_db_svm() {
    local current_time
    current_time=$(get_timestamp)
    local svm_uuid
    svm_uuid=$(generate_uuid)

    log_info "Creating VCP database SVM"

    # Insert SVM entry
    local svm_insert_sql="
        INSERT INTO svms (
            uuid, created_at, updated_at, name, state, state_details,
            svm_details, pool_id, account_id
        ) VALUES (
            '$svm_uuid', '$current_time', '$current_time', '$SVM_NAME', 'READY',
            'Available for use',
            '{\"ip_space\": \"Default\", \"external_uuid\": \"00000000-0000-0000-0000-000000000000\"}',
            $POOL_ID, $ACCOUNT_ID
        );
    "

    psql "$POSTGRES_CONNECTION_STRING" -c "$svm_insert_sql"

    # Get SVM ID
    SVM_ID=$(psql "$POSTGRES_CONNECTION_STRING" -t -c "SELECT id FROM svms WHERE uuid='$svm_uuid';" | xargs)

    log_info "Created SVM with ID: $SVM_ID"
}

create_db_nodes() {
    local region="$1"
    local ip_address="$2"
    local current_time
    current_time=$(get_timestamp)

    log_info "Creating VCP database nodes"

    # Create a node for each VSIM configuration
    NODE_IDS=()
    for i in "${!VSIM_CONFIGS[@]}"; do
        local node_uuid
        node_uuid=$(generate_uuid)
        local node_name="vcp-node-$i"

        local node_insert_sql="
            INSERT INTO nodes (
                uuid, created_at, updated_at, name, state, state_details,
                pool_id, \"endpoint_Address\", host_dns_name, zone_name, account_id
            ) VALUES (
                '$node_uuid', '$current_time', '$current_time', '$node_name', 'READY',
                'Available for use', $POOL_ID, '$ip_address', '$ip_address',
                '${region}-a', $ACCOUNT_ID
            );
        "

        psql "$POSTGRES_CONNECTION_STRING" -c "$node_insert_sql"
    done

    NODE_IDS=()
    while IFS= read -r node_id; do
        NODE_IDS+=("$node_id")
    done < <(psql "$POSTGRES_CONNECTION_STRING" -t -c "SELECT id FROM nodes WHERE pool_id=$POOL_ID ORDER BY id;" | xargs -n1)

    log_info "Created nodes with IDs: ${NODE_IDS[*]}"
}

create_db_lifs() {
    local current_time
    current_time=$(get_timestamp)
    local empty_uuid="00000000-0000-0000-0000-000000000000"

    log_info "Creating VCP database LIFs"

    for i in "${!NODE_IDS[@]}"; do
        local node_id="${NODE_IDS[$i]}"
        local ip_var="VSIM$((i+1))_DATA_IP1"
        local ip_address="${!ip_var}"
        local lif_uuid
        lif_uuid=$(generate_uuid)
        local lif_name="vcp-lif-$i"

        local lif_insert_sql="
            INSERT INTO lifs (
                uuid, created_at, updated_at, name, node_id, ip_address,
                subnet_mask, lif_details, account_id
            ) VALUES (
                '$lif_uuid', '$current_time', '$current_time', '$lif_name', $node_id,
                '$ip_address', '255.255.255.255',
                '{\"external_uuid\": \"$empty_uuid\", \"protocol_type\": \"nas\"}',
                $ACCOUNT_ID
            );
        "

        psql "$POSTGRES_CONNECTION_STRING" -c "$lif_insert_sql"
    done

    log_info "Created LIFs for nodes"
}

# =============================================================================
# ONTAP API FUNCTIONS
# =============================================================================

ontap_api_call() {
    local cluster_mgmt_ip="$1"
    local endpoint="$2"
    local method="${3:-GET}"
    local payload="${4:-}"

    local auth_header="Authorization: Basic $(echo -n "admin:${ONTAP_PASSWORD}" | base64)"
    local api_url="https://${cluster_mgmt_ip}/api/${endpoint}"

    log_debug "Making $method API request to: $api_url"

    local curl_args=(-s -k -w "\n%{http_code}")
    curl_args+=(-H "Accept: application/json")
    curl_args+=(-H "$auth_header")
    curl_args+=(-H "Content-Type: application/json")

    if [[ "$method" != "GET" ]]; then
        curl_args+=(-X "$method")
    fi

    if [[ -n "$payload" ]]; then
        curl_args+=(-d "$payload")
    fi

    curl_args+=("$api_url")

    local response
    response=$(curl "${curl_args[@]}" 2>/dev/null || echo -e "\ncurl_failed")

    local http_code
    http_code=$(echo "$response" | tail -n1)
    local response_body
    response_body=$(echo "$response" | sed '$d')

    log_debug "HTTP response code: $http_code"
    log_debug "API response body: $response_body"

    # Return the response body and http code as global variables
    ONTAP_RESPONSE_BODY="$response_body"
    ONTAP_HTTP_CODE="$http_code"

    return 0
}

validate_ontap_connectivity() {
    local cluster_mgmt_ip="$1"

    log_debug "Validating ONTAP connectivity to cluster: $cluster_mgmt_ip"

    if [[ -z "${ONTAP_PASSWORD:-}" ]]; then
        log_error "ONTAP_PASSWORD environment variable is not set"
        log_error "Please set ONTAP_PASSWORD before running this script"
        exit 1
    fi

    ontap_api_call "$cluster_mgmt_ip" "cluster"

    if [[ "$ONTAP_HTTP_CODE" != "200" ]]; then
        if [[ "$ONTAP_HTTP_CODE" == "401" ]]; then
            log_error "Authentication failed to ONTAP cluster at $cluster_mgmt_ip"
            log_error "Please check the ONTAP_PASSWORD environment variable"
        else
            log_error "ONTAP API request failed with HTTP code: $ONTAP_HTTP_CODE"
            log_error "Response: $ONTAP_RESPONSE_BODY"
        fi
        return 1
    fi

    local cluster_name
    cluster_name=$(echo "$ONTAP_RESPONSE_BODY" | jq -r '.name // "unknown"')
    local cluster_version
    cluster_version=$(echo "$ONTAP_RESPONSE_BODY" | jq -r '.version.full // "unknown"')

    log_info "ONTAP connectivity validated successfully"
    log_info "Cluster name: $cluster_name, Version: $cluster_version"

    return 0
}

query_ontap_svms() {
    local cluster_mgmt_ip="$1"

    log_debug "Querying ONTAP API for all SVMs on cluster: $cluster_mgmt_ip"

    ontap_api_call "$cluster_mgmt_ip" "svm/svms"

    local svm_names
    svm_names=$(echo "$ONTAP_RESPONSE_BODY" | jq -r '.records[]?.name // empty' | tr '\n' ' ')

    if [[ -n "$svm_names" ]]; then
        log_info "Found SVMs on ONTAP cluster: $svm_names"
        echo "$svm_names" | awk '{print $1}'
        return 0
    else
        log_warn "No SVMs found on ONTAP cluster"
        return 1
    fi
}

# Create SVM on ONTAP cluster
create_ontap_svm() {
    local cluster_mgmt_ip="$1"
    local svm_name="$2"

    log_info "Creating SVM '$svm_name' on ONTAP cluster"

    # SVM creation payload
    local svm_payload
    svm_payload=$(jq -n \
        --arg name "$svm_name" \
        --arg ipspace "Default" \
        '{
            name: $name,
            ipspace: {name: $ipspace},
            nfs: {enabled: true},
            cifs: {enabled: false}
        }')

    log_debug "SVM creation payload: $svm_payload"

    # Make API call to create SVM
    ontap_api_call "$cluster_mgmt_ip" "svm/svms" "POST" "$svm_payload"

    if [[ "$ONTAP_HTTP_CODE" == "202" ]]; then
        log_info "Successfully started SVM creation for '$svm_name'"
    else
        log_error "Failed to create SVM '$svm_name'. HTTP code: $ONTAP_HTTP_CODE"
        log_error "Response: $ONTAP_RESPONSE_BODY"
        return 1
    fi

    # Poll for SVM to become active
    local max_retries=10
    local retry_count=0
    local sleep_interval=5

    while [[ $retry_count -lt $max_retries ]]; do
        sleep "$sleep_interval"
        ontap_api_call "$cluster_mgmt_ip" "svm/svms?name=$svm_name&fields=name,state"

        local svm_state
        svm_state=$(echo "$ONTAP_RESPONSE_BODY" | jq -r '.records[0]?.state // "unknown"')

        if [[ "$svm_state" == "running" ]]; then
            log_info "SVM '$svm_name' is now active"
            return 0
        else
            log_info "Waiting for SVM '$svm_name' to become active (current state: $svm_state)"
        fi

        ((retry_count++))
    done

    return 1
}

# Discover and manage SVM
discover_svm() {
    local cluster_mgmt_ip="$1"

    log_info "Discovering and managing SVM configuration"

    # Discover existing SVMs
    local existing_svm
    existing_svm=$(query_ontap_svms "$cluster_mgmt_ip")

    if [[ $? -eq 0 && -n "$existing_svm" ]]; then
        log_info "Using existing SVM: $existing_svm"
        SVM_NAME="$existing_svm"
    else
        log_warn "No existing SVMs found - creating SVM: $SVM_NAME"
        if create_ontap_svm "$cluster_mgmt_ip" "$SVM_NAME"; then
            log_info "Successfully created SVM: $SVM_NAME"
        else
            log_error "Failed to create SVM: $SVM_NAME"
            return 1
        fi
    fi

    return 0
}

# Query ONTAP API for SVM network interfaces
query_ontap_svm_interfaces() {
    local cluster_mgmt_ip="$1"
    local svm_name="$2"

    log_debug "Querying ONTAP API for SVM network interfaces: $svm_name"

    # Make API call to network interfaces endpoint with SVM filter
    ontap_api_call "$cluster_mgmt_ip" "network/ip/interfaces?svm.name=${svm_name}"

    # Parse the JSON response to get interface information
    local interface_count
    interface_count=$(echo "$ONTAP_RESPONSE_BODY" | jq -r '.num_records // 0')

    if [[ "$interface_count" -gt 0 ]]; then
        local interface_names
        interface_names=$(echo "$ONTAP_RESPONSE_BODY" | jq -r '.records[]?.name // empty' | tr '\n' ' ')
        log_info "Found $interface_count network interface(s) for SVM '$svm_name': $interface_names"
        return 0
    else
        log_warn "No network interfaces found for SVM '$svm_name'"
        return 1
    fi
}

# Create network interface for SVM
create_ontap_svm_interface() {
    local cluster_mgmt_ip="$1"
    local svm_name="$2"
    local interface_name="$3"
    local ip_address="$4"
    local netmask="${5:-255.255.255.0}"

    log_info "Creating network interface '$interface_name' for SVM '$svm_name' with IP $ip_address"

    # Interface creation payload
    local interface_payload
    interface_payload=$(jq -n \
        --arg name "$interface_name" \
        --arg svm_name "$svm_name" \
        --arg ip "$ip_address" \
        --arg netmask "$netmask" \
        '{
            name: $name,
            svm: {name: $svm_name},
            ip: {
                address: $ip,
                netmask: $netmask
            },
            location: {
                broadcast_domain: {name: "Default"},
            },
            service_policy: {name: "default-data-files"}
        }')

    log_debug "Interface creation payload: $interface_payload"

    # Make API call to create interface
    ontap_api_call "$cluster_mgmt_ip" "network/ip/interfaces" "POST" "$interface_payload"

    if [[ "$ONTAP_HTTP_CODE" == "201" ]]; then
        log_info "Successfully created network interface '$interface_name' with IP $ip_address"
        return 0
    else
        log_error "Failed to create network interface '$interface_name'. HTTP code: $ONTAP_HTTP_CODE"
        log_error "Response: $ONTAP_RESPONSE_BODY"
        return 1
    fi
}

# Discover and create SVM network interfaces if needed
discover_svm_interfaces() {
    local cluster_mgmt_ip="$1"
    local svm_name="$2"

    log_info "Discovering SVM network interfaces for '$svm_name'"

    # Check if interfaces already exist
    if query_ontap_svm_interfaces "$cluster_mgmt_ip" "$svm_name"; then
        log_info "SVM '$svm_name' already has network interfaces configured"
        return 0
    fi

    # Get DATA_IP addresses from both configs
    local data_ip1="${VSIM1_DATA_IP1:-}"
    local data_ip2="${VSIM2_DATA_IP1:-}"

    if [[ -z "$data_ip1" ]]; then
        log_error "DATA_IP1 not found in first VSIM configuration"
        return 1
    fi

    if [[ -z "$data_ip2" ]]; then
        log_error "DATA_IP1 not found in second VSIM configuration"
        return 1
    fi

    log_info "Creating network interfaces using DATA_IP1 from both configs: $data_ip1, $data_ip2"

    # Create interface for first node
    local interface1_name="${svm_name}_data_1"
    if ! create_ontap_svm_interface "$cluster_mgmt_ip" "$svm_name" "$interface1_name" "$data_ip1"; then
        log_error "Failed to create first network interface"
        return 1
    fi

    # Create interface for second node
    local interface2_name="${svm_name}_data_2"
    if ! create_ontap_svm_interface "$cluster_mgmt_ip" "$svm_name" "$interface2_name" "$data_ip2"; then
        log_error "Failed to create second network interface"
        return 1
    fi

    log_info "Successfully created network interfaces for SVM '$svm_name'"
    return 0
}

# Discover ONTAP information and create resources if needed
discover_ontap_info() {
    log_info "Discovering ONTAP cluster information and resources"

    # Get cluster management IP from vsim_values1
    local cluster_mgmt_ip="${VSIM1_CLUSTER_MGMT_IP:-}"

    if [[ -z "$cluster_mgmt_ip" ]]; then
        log_error "cluster_mgmt_ip not found in VSIM configuration"
        log_error "Please ensure the VSIM configuration file contains cluster_mgmt_ip"
        exit 1
    fi

    log_info "Using cluster management IP: $cluster_mgmt_ip"

    # Step 1: Validate ONTAP connectivity
    if ! validate_ontap_connectivity "$cluster_mgmt_ip"; then
        log_error "Failed to validate ONTAP connectivity"
        exit 1
    fi

    # Step 2: Discover and manage SVM
    if ! discover_svm "$cluster_mgmt_ip"; then
        log_error "Failed to configure SVM"
        exit 1
    fi

    # Step 3: Discover and create SVM network interfaces
    if ! discover_svm_interfaces "$cluster_mgmt_ip" "$SVM_NAME"; then
        log_error "Failed to configure SVM network interfaces"
        exit 1
    fi

    log_info "ONTAP discovery and resource preparation completed"
}

# =============================================================================
# MAIN ORCHESTRATION FUNCTIONS
# =============================================================================

# Main function to create VCP database entries
create_vcp_db_entries() {
    local region="$REGION"
    local cluster_mgmt_ip="${VSIM1_CLUSTER_MGMT_IP:-}"

    log_info "Creating VCP database entries for VSIM with IP $cluster_mgmt_ip"

    create_or_verify_account
    create_db_pool "$region"
    create_db_svm
    create_db_nodes "$region" "$cluster_mgmt_ip"
    create_db_lifs

    log_info "Successfully created VCP database entries for VSIM with IP $cluster_mgmt_ip"
}

# Main execution function
main() {
    log_info "Starting connect VSIM (VCP) command"

    parse_args "$@"
    check_dependencies
    extract_all_vsim_values
    configure_postgres_connection
    discover_ontap_info
    create_vcp_db_entries

    log_info "VSIM VCP connection completed successfully"
}

# Execute main function with all arguments
main "$@"