# Running in CI

The sandbox is one container with a healthcheck, so CI systems can start it as a
service next to the job. The two things to get right are the same everywhere:

- **Callbacks need a way back.** The sandbox runs in its own container and sends
  callbacks to `PSP_CALLBACK_URL`. That URL must name your app as the sandbox container
  sees it, not as your tests see it. `localhost` inside the sandbox is the sandbox.
- **Retries must be short.** The default `PSP_RETRY_SCHEDULE` retries for an hour. Set
  `0s` (one attempt) or something like `0s,1s,2s`, so a broken handler fails the job in
  seconds.

If the job already runs `docker compose`, none of this is new: add the sandbox to the
compose file as in the [README](../README.md#quick-start) and use
`docker compose up --wait`, which waits for the healthcheck. The
[examples](../examples) run that way in this repository's own CI.

## GitHub Actions

Service containers start before the first step, and the runner waits until the
image's healthcheck reports healthy. Tests on the runner reach the sandbox on
`localhost`:

```yaml
jobs:
  test:
    runs-on: ubuntu-latest
    services:
      psp:
        image: ghcr.io/ianfoxdev/psp-sandbox:0.4
        ports: ["8090:8090"]
        env:
          PSP_CALLBACK_URL: http://host.docker.internal:8000/api/psp/callback
          PSP_WEBHOOK_SECRET: whsec_dGVzdC1zZWNyZXQ=
          PSP_RETRY_SCHEDULE: 0s,1s,2s
        # lets the sandbox reach the app that runs on the runner itself
        options: --add-host=host.docker.internal:host-gateway
    env:
      PSP_URL: http://localhost:8090
    steps:
      - uses: actions/checkout@v7
      - run: composer install
      - run: php artisan serve --host=0.0.0.0 --port=8000 &
      - run: vendor/bin/phpunit
```

The app has to listen on `0.0.0.0`, not `127.0.0.1`: callbacks come from the Docker
network, not from the runner's loopback.

For the Stripe profile add `PSP_PROFILE: stripe` and point the SDK at
`http://localhost:8090` (see [stripe.md](stripe.md)).

## GitLab CI

With the Docker executor, services listed in a job start before the script and share
its CI/CD variables. The runner waits until the exposed port `8090` accepts
connections. The job reaches the sandbox by its alias:

```yaml
test:
  image: php:8.4
  services:
    - name: ghcr.io/ianfoxdev/psp-sandbox:0.4
      alias: psp
  variables:
    # one network per job, so the sandbox can call back into the job container
    FF_NETWORK_PER_BUILD: "1"
    PSP_CALLBACK_URL: http://build:8000/api/psp/callback
    PSP_WEBHOOK_SECRET: whsec_dGVzdC1zZWNyZXQ=
    PSP_RETRY_SCHEDULE: 0s,1s,2s
    PSP_URL: http://psp:8090
  script:
    - composer install
    - php -S 0.0.0.0:8000 -t public &
    - vendor/bin/phpunit
```

`build` is the name GitLab Runner gives the job container on that network. Without
`FF_NETWORK_PER_BUILD` the sandbox has no name to reach the job by, and every delivery
ends with a connection `error`.

Job variables go to every service and to the job itself. Other services and your app
ignore the `PSP_*` ones.

## Waiting for the sandbox yourself

Where a CI system starts containers without waiting (a plain `docker run -d`, a shell
executor), poll the health endpoint before the tests:

```bash
for i in $(seq 1 30); do
  curl -fsS http://localhost:8090/healthz && break
  sleep 1
done
```

The image has a `HEALTHCHECK`, so
`docker inspect --format '{{.State.Health.Status}}' <container>` works too.

## Several jobs, one sandbox

Each CI job gets its own service container, so jobs do not see each other's payments.
Tests that run in parallel inside one job (paratest) share it: give every test a
reference prefix and reset only that, see [Parallel tests](api.md#parallel-tests).

## Browser tests

The 3DS page and the Checkout page (Stripe profile) are links the sandbox builds from
`PSP_PUBLIC_URL`. Set it to the address the browser uses: `http://localhost:8090` for a
browser on the runner, `http://psp:8090` for a headless browser in a service or compose
container next to the sandbox.
