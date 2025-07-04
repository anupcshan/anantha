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

### Run the container
```bash
docker run -p 53:53/udp -p 53:53/tcp -p 80:80 -p 443:443 -p 8883:8883 -p 26268:26268 \
  ghcr.io/anupcshan/anantha:latest serve \
  --ntp-addr <NTP_IP> \
  --ha-mqtt-addr <HA_MQTT_IP> \
  --ha-mqtt-topic-prefix <HA_MQTT_TOPIC_PREFIX> \
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
    restart: unless-stopped
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
