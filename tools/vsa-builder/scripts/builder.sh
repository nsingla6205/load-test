#!/bin/bash
# shellcheck disable=SC2174
set -Eeuo pipefail

CURL_OPTS="--connect-timeout 60 --max-time 120 --speed-time 30 --speed-limit 1024 --retry 5 --retry-delay 60 --retry-all-errors"
PACKAGES=(
  apt-transport-https
  build-essential
  ca-certificates
  gawk
  gnupg
  jq
  lsb-release
  openssh-client
  sudo
  tree
  unzip
  xz-utils
  zip
  zstd
  wget
  python3
  python3-pip
  fping
  uuid-runtime
  iputils-ping
  bind9-host
  bind9-utils
  bind9-dnsutils
  file
  yamllint
  gpg
 
)

export NEEDRESTART_MODE=a
export DEBIAN_FRONTEND=noninteractive
export DEBIAN_PRIORITY=critical

## useradd
printf "\n\t🐋 Create runner user 🐋\n"
groupadd -g 1001 github
groupadd -g 998 docker
useradd -u 1001 -g github -m -s /bin/bash github
usermod -a -G docker github
printf "\n\t🐋 User info 🐋\n"
su - github -c id
grep github /etc/passwd

## apt
printf "\n\t🐋 Update source.list to use repomirror.rtp.openeng.netapp.com\n"
sed -i -e 's/security\.ubuntu\.com/repomirror\.rtp\.openeng\.netapp\.com/g' /etc/apt/sources.list
sed -i -e 's/archive\.ubuntu\.com/repomirror\.rtp\.openeng\.netapp\.com/g' /etc/apt/sources.list

## install curl
apt-get -qy update
apt-get -qy install curl

## install git from ppa
curl -fsSL ${CURL_OPTS} -o /etc/apt/keyrings/git-ppa.pub "https://keyserver.ubuntu.com/pks/lookup?op=get&search=0xe1dd270288b4e6030699e45fa1715d88e1df1f24"
echo "deb [signed-by=/etc/apt/keyrings/git-ppa.pub] https://ppa.launchpadcontent.net/git-core/ppa/ubuntu jammy main" | tee -a /etc/apt/sources.list.d/gpg-ppa.list
apt-get -qy update
apt-get -qy install git
# git version 2.35.2 introduces security fix that breaks action\checkout https://github.com/actions/checkout/issues/760
cat <<EOF >>/etc/gitconfig
[safe]
        directory = *
EOF
printf "\n\t🐋 git version🐋\n"
git --version

## install package collection
printf "\n\t🐋 Install packages 🐋\n"
apt-get -yq install --no-install-recommends --option=Dpkg::Options::=--force-confdef "${PACKAGES[@]}"

## sudoers
printf "\n\t🐋 Update sudoers🐋\n"
usermod -a -G sudo github
echo "github ALL=(ALL) NOPASSWD: ALL" >>/etc/sudoers

## install gcloud
curl -fsSL ${CURL_OPTS} https://packages.cloud.google.com/apt/doc/apt-key.gpg | sudo apt-key --keyring /usr/share/keyrings/cloud.google.gpg add -
echo "deb [signed-by=/usr/share/keyrings/cloud.google.gpg] https://packages.cloud.google.com/apt cloud-sdk main" | tee /etc/apt/sources.list.d/google-cloud-sdk.list
apt-get -qy update && apt-get -qy install google-cloud-cli
gcloud version

## install golangci-lint
# v2.9.0 is built with go1.26, required to lint a module targeting go1.26.x
# (golangci-lint must be built with a Go version >= the analyzed go.mod version).
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b $(go env GOPATH)/bin v2.9.0
golangci-lint version

# Install required packages
apt-get -qy update
apt-get -qy install gnupg lsb-release

## install docker
printf "\n\t🐋 Install Docker🐋\n"
curl -fsSL https://get.docker.com -o get-docker.sh
sh get-docker.sh
if docker --version; then
  printf "Docker installed"
else
  printf "::error:: Failed to install Docker"
  exit 1
fi

# Install jre
printf "\n\t🐋 Install jre 🐋\n"
apt-get -qy update && apt-get -qy install default-jre

# Install helm
printf "\n\t🐋 Install helm 3 🐋\n"
curl -fsSL -o get_helm.sh https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 && chmod 700 get_helm.sh
./get_helm.sh

## install yq
printf "\n\t🐋 Install yq 🐋\n"
if curl -fsSL ${CURL_OPTS} https://github.com/mikefarah/yq/releases/download/v4.40.4/yq_linux_amd64 -o /usr/bin/yq; then
  chmod +x /usr/bin/yq
  printf "yq installed"
else
  printf "::error:: Failed to install yq"
  exit 1
fi

## install github cli
printf "\n\t🐋 Install GitHub CLI 🐋\n"
curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | sudo dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg
sudo chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | sudo tee /etc/apt/sources.list.d/github-cli.list > /dev/null
sudo apt update
sudo apt install gh -y
if gh --version; then
  printf "gh cli installed\n"
else
  printf "::error:: Failed to install gh cli\n"
  exit 1
fi

## install rclone
printf "\n\t🐋 Install Rclone 🐋\n"
curl https://rclone.org/install.sh | bash

# jira-cli https://github.com/ankitpokhrel/jira-cli
# install jira-cli
#printf "\n\t🐋 Install Jira CLI 🐋\n"
#JIRA_CLI_URL=$(curl -s https://api.github.com/repos/ankitpokhrel/jira-cli/releases/latest | jq -r '.assets[] | select(.name | endswith("linux_amd64.tar.gz")) | .browser_download_url')
#if [ -z "$JIRA_CLI_URL" ]; then
#  printf "::error:: Failed to retrieve Jira CLI download URL\n"
#  exit 1
#fi

# deno install
printf "\n\t🐋 Install deno🐋\n"
if curl ${CURL_OPTS} -fsSL https://deno.land/install.sh | DENO_INSTALL=/usr/local sh; then
  printf "deno installed"
else
  printf "::error:: Failed to install Deno"
  exit 1
fi

# skaffold install
printf "\n\t🐋 Install skaffold🐋\n"
if curl -Lo skaffold https://storage.googleapis.com/skaffold/releases/latest/skaffold-linux-amd64 && sudo install skaffold /usr/local/bin/; then
  printf "skaffold installed"
else
  printf "::error:: Failed to install skaffold"
  exit 1
fi

# cosign install
printf "\n\t Install cosign\n"
if curl -O -L "https://github.com/sigstore/cosign/releases/latest/download/cosign-linux-amd64" && sudo mv cosign-linux-amd64 /usr/local/bin/cosign && sudo chmod +x /usr/local/bin/cosign; then
  printf "cosign installed"
else
  printf "::error:: Failed to install cosign"
  exit 1
fi

# ORAS CLI install (pinned version + checksum for supply-chain integrity)
# Used to push binaries as OCI artifacts to OCI registries (e.g. GHCR).
printf "\n\t🐋 Install ORAS CLI 🐋\n"
ORAS_VERSION="1.2.0"
ORAS_CHECKSUM="5b3f1cbb86d869eee68120b9b45b9be983f3738442f87ee5f06b00edd0bab336"
ORAS_TARBALL="oras_${ORAS_VERSION}_linux_amd64.tar.gz"
if curl -fsSL ${CURL_OPTS} -o "/tmp/${ORAS_TARBALL}" "https://github.com/oras-project/oras/releases/download/v${ORAS_VERSION}/${ORAS_TARBALL}" && \
   echo "${ORAS_CHECKSUM}  /tmp/${ORAS_TARBALL}" | sha256sum --check --strict && \
   tar -xzf "/tmp/${ORAS_TARBALL}" -C /usr/local/bin oras && \
   rm -f "/tmp/${ORAS_TARBALL}" && \
   oras version; then
  printf "oras installed\n"
else
  printf "::error:: Failed to install oras\n"
  exit 1
fi

# install gosu https://github.com/tianon/gosu
GOSU_URL="https://github.com/tianon/gosu/releases/download/1.14/gosu-amd64"
GOSU_ASC_URL="${GOSU_URL}.asc"
curl ${CURL_OPTS} -fsSL -o /usr/local/bin/gosu "${GOSU_URL}"
curl ${CURL_OPTS} -fsSL -o ./gosu.asc "${GOSU_ASC_URL}"
gpg --batch --keyserver hkps://keys.openpgp.org --recv-keys B42F6819007F00F88E364FD4036A9C25BF357DD4
gpg --batch --verify gosu.asc /usr/local/bin/gosu
chmod +x /usr/local/bin/gosu

## cleanup
printf "\n\t🐋 Cleanup 🐋\n"
apt-get -qy clean
rm -rf /var/cache/* /var/log/* /var/lib/apt/lists/* /tmp/*

printf "\n\t🐋 Finished building 🐋\n"
