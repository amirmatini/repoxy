# Repoxy

Reverse-proxy cache for package repositories (APT, YUM, APK).

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/amirmatini/repoxy/main/install.sh | sudo bash
```

## Config

`/etc/repoxy.yaml`:

```yaml
server:
  listeners:
    - addr: ":8080"

cache:
  dir: "/var/cache/repoxy"
  max_size_bytes: "200GB"

upstreams:
  ubuntu:
    base_url: "https://archive.ubuntu.com/ubuntu"
    path_prefix: "/ubuntu"
```

## Usage

### Supported Package Managers

- **APT** (Debian, Ubuntu, Mint)
- **YUM/DNF** (RHEL, CentOS, AlmaLinux, Rocky, Fedora)
- **APK** (Alpine Linux)
- **Pacman** (Arch Linux, Manjaro)
- **Zypper** (openSUSE, SUSE)
- **Generic HTTP** repositories

### Examples

**APT (Debian/Ubuntu):**
```bash
# /etc/apt/sources.list
deb http://cache:8080/ubuntu jammy main universe
deb http://cache:8080/ubuntu jammy-security main
deb http://cache:8080/debian bookworm main contrib
```

**YUM/DNF (RHEL/CentOS/AlmaLinux):**
```bash
# /etc/yum.repos.d/almalinux.repo
[baseos]
baseurl=http://cache:8080/almalinux/9/BaseOS/x86_64/os/

[appstream]
baseurl=http://cache:8080/almalinux/9/AppStream/x86_64/os/
```

**APK (Alpine):**
```bash
# /etc/apk/repositories
http://cache:8080/alpine/v3.18/main
http://cache:8080/alpine/v3.18/community
```

**Pacman (Arch Linux):**
```bash
# /etc/pacman.d/mirrorlist
Server = http://cache:8080/archlinux/$repo/os/$arch
```

**Zypper (openSUSE):**
```bash
sudo zypper ar http://cache:8080/opensuse/distribution/leap/15.5/repo/oss/ oss
```

## Monitoring

```bash
curl http://cache:8080/_healthz   # health
curl http://cache:8080/_stats     # stats
curl http://cache:8080/_metrics   # prometheus
```

## Advanced

### TLS

```yaml
server:
  listeners:
    - addr: ":443"
      tls:
        cert_file: "/etc/ssl/cert.pem"
        key_file: "/etc/ssl/key.pem"
```

### Authentication

```yaml
# Upstream auth
upstreams:
  private:
    base_url: "https://private.example.com"
    headers:
      Authorization: "Bearer TOKEN"

# Ingress auth
auth:
  enabled: true
  type: "basic"
  users:
    admin: "password"
```

### Egress Proxy

```yaml
proxy:
  enabled: true
  type: "socks5"
  url: "socks5://proxy:1080"
```

### Policies

```yaml
policies:
  - name: "debs"
    regex: "\\.(deb|udeb)$"
    cache_ttl: "30d"
```

### Purge

```yaml
admin:
  enable_purge_api: true
  token: "secret"
```

```bash
curl -X POST http://cache:8080/_purge/by-url \
  -H "Authorization: Bearer secret" \
  -d '{"url": "https://..."}'
```

## Build

```bash
git clone https://github.com/amirmatini/repoxy
cd repoxy
make build
```

## License

MIT
