# Demo deployment

A small public image for trying a deployment by hand: it exercises the image source, the container port, environment
variables and service access. It is a scratch aid for contributors, not product documentation.

```text
mendhak/http-https-echo:41
```

## Deployment settings

- Image: `mendhak/http-https-echo:41`
- Container port: `8080`
- Protocol: `HTTP`
- Replicas: `1`
- Command: leave blank
- Entrypoint: leave blank

## Environment variables

```text
ECHO_INCLUDE_ENV_VARS=1
APP_NAME=geass-demo
APP_ENV=staging
FEATURE_FLAG=true
```

After deployment, open the service URL. The response includes request details and an `env` object containing the
configured variables.

Do not add real secrets: this demo image intentionally exposes environment variables in its response.

Documentation: <https://github.com/mendhak/docker-http-https-echo#readme>
