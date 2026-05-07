# Docker Setup for Anantha

This repository now includes automated Docker image building and publishing using GitHub Actions.

## How it works

Every commit to the `main`, `master`, or `develop` branches will automatically:

1. Build a multi-architecture Docker image (linux/amd64 and linux/arm64)
2. Run tests to ensure code quality
3. Push the image to GitHub Container Registry
4. Tag the image with both commit SHA and `latest` (for main branch)

## Image Location

The Docker images are published to:
```
ghcr.io/anupcshan/anantha:latest
ghcr.io/anupcshan/anantha:<branch>-<commit-sha>
```

## Using the Docker Image

### Pull the image
```bash
docker pull ghcr.io/anupcshan/anantha:latest
```

### Run the server (`serve`)
```bash
docker run -p 53:53/udp -p 53:53/tcp -p 80:80 -p 443:443 -p 8883:8883 -p 26268:26268 \
  ghcr.io/anupcshan/anantha:latest serve \
  --ntp-addr <NTP_IP> \
  --ha-mqtt-addr <HA_MQTT_IP> \
  --ha-mqtt-topic-prefix <HA_MQTT_TOPIC_PREFIX> \
  --ha-mqtt-username <HA_MQTT_USERNAME> \
  --ha-mqtt-password <HA_MQTT_PASSWORD> \
  --client-id <THERMOSTAT_DEVICE_ID> \
  --external-ip-override <DOCKER_HOST_IP> # Optional
```

### Patch firmware (`edit-firmware`)

The image also bundles the `edit-firmware` subcommand for patching thermostat firmware files. Run as a one-shot, mounting the host directory containing the input/output hex files:

```bash
docker run --rm \
  -v /path/to/firmware:/firmware \
  ghcr.io/anupcshan/anantha:latest edit-firmware \
  -in /firmware/BINF0456.hex \
  -out /firmware/BINF0456.patched.hex
```

`--rm` makes the container transient; the bind mount lets the patched output land back on the host.

### Clear stale HA discovery (`reset-ha-discovery`)

One-time migration helper for users upgrading from a version of anantha before the `device` block was added to Home Assistant MQTT discovery. See the "Upgrading" section in the main README for context. Stop the running `anantha serve` container first, run this one-shot, then start `serve` again.

```bash
docker run --rm ghcr.io/anupcshan/anantha:latest reset-ha-discovery \
  --ha-mqtt-addr <HA_MQTT_IP>:1883 \
  --ha-mqtt-username <HA_MQTT_USERNAME> \
  --ha-mqtt-password <HA_MQTT_PASSWORD> \
  --client-id <THERMOSTAT_DEVICE_ID>
```

### Docker Compose Example
```yaml
version: '3.8'
services:
  anantha:
    image: ghcr.io/anupcshan/anantha:latest
    ports:
      - "53:53/udp"
      - "53:53/tcp"
      - "80:80"
      - "443:443"
      - "8883:8883"
      - "26268:26268"
    command: >
      serve
      --ntp-addr 192.168.1.1
      --ha-mqtt-addr 192.168.1.100
      --ha-mqtt-topic-prefix hvac/carrier
      --ha-mqtt-username HA_MQTT_USERNAME
      --ha-mqtt-password HA_MQTT_PASSWORD
      --client-id YOUR_THERMOSTAT_ID
      --external-ip-override DOCKER_HOST_IP
    restart: unless-stopped
```

To run the one-shot subcommands against the same compose service definition, use `docker compose run --rm` and override the command. Stop the long-running `anantha` service first when running `reset-ha-discovery`.

```bash
# Patch firmware via compose:
docker compose run --rm \
  -v /path/to/firmware:/firmware \
  anantha edit-firmware -in /firmware/BINF0456.hex -out /firmware/BINF0456.patched.hex

# Clear stale HA discovery via compose (stop the service first):
docker compose stop anantha
docker compose run --rm anantha reset-ha-discovery \
  --ha-mqtt-addr 192.168.1.100:1883 \
  --ha-mqtt-username HA_MQTT_USERNAME \
  --ha-mqtt-password HA_MQTT_PASSWORD \
  --client-id YOUR_THERMOSTAT_ID
docker compose up -d anantha
```

## Building Locally

To build the Docker image locally:
```bash
docker build -t anantha .
```

## Files Created

- `Dockerfile`: Multi-stage build configuration
- `.dockerignore`: Optimizes build context
- `.github/workflows/docker.yml`: GitHub Actions workflow
- `DOCKER.md`: This documentation file

## Security Features

- Images are built with security attestation
- Uses non-root user in container
- Minimal Debian base image
- Only necessary files are included
