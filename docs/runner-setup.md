# Gitea Actions runner setup

This guide sets up two runners to build notus-swap: a **main** one on a fast machine, and a **backup** one on a second machine. The main runner picks up jobs first because it asks Gitea for work every 2 seconds, while the backup asks every 30 seconds. Gitea has no runner priority setting, so this is how the faster machine gets the jobs. One runner is enough if you only have one machine.

This guide uses [`gitea/runner`](https://gitea.com/gitea/runner), which is the new name for `act_runner`.

Do every step below on **both machines**. Only these settings differ between them:

| Setting | Main | Backup |
|---|---|---|
| `GITEA_RUNNER_NAME` | the machine's name | the machine's name |
| `runner.fetch_interval` and `runner.fetch_interval_max` | `2s` | `30s` |
| `runner.capacity` | `1` | `1` (or `2`, if you like) |

`fetch_interval_max` must match `fetch_interval`. Otherwise an idle runner slowly backs off to asking every 5 seconds, and the main runner loses its head start.

## Choices to make first

- **Where to register the runner:**
  - *Repository level (recommended):* only notus-swap's jobs run on it. Get the token from the repo page, under Settings → Actions → Runners → Create new runner.
  - *Instance level:* every repo on your Gitea can use it. Get the token from Site Administration → Actions → Runners.

  A runner has control of Docker on its host, which is close to root access. Repository level keeps that limited to code you wrote.
- **Runner version:** this guide pins `3.5.0`, the newest release on 2026-09-24, since you don't auto-update. To upgrade later, change the tag in `docker-compose.yml`. Newer tags are listed at https://hub.docker.com/r/gitea/runner/tags.

## 1. Check that Gitea Actions is on

Open the notus-swap repo, then go to Settings → Actions. If there is no Actions section, turn Actions on in Gitea's `app.ini`:

```ini
[actions]
ENABLED = true
```

Then restart Gitea however you normally do.

## 2. Make the folder

```bash
mkdir -p ~/Applications/gitea-runner/data
cd ~/Applications/gitea-runner
```

## 3. Generate the runner's config file

Use a `gitea_runner` shell function to avoid repeating the `docker run` command. It works in bash and zsh; `gr` would clash with an oh-my-zsh alias. Run this from `~/Applications/gitea-runner`:

```bash
gitea_runner() { docker run --rm --user "$(id -u):$(id -g)" --entrypoint gitea-runner -v "$PWD":/work -w /work docker.io/gitea/runner:3.5.0 "$@"; }
```

Create an empty `config.yaml`, then set the values. On the main runner:

```bash
gitea_runner config init
gitea_runner config set runner.capacity 1
gitea_runner config set runner.fetch_interval 2s
gitea_runner config set runner.fetch_interval_max 2s
gitea_runner config add runner.labels 'ubuntu-latest:docker://docker.gitea.com/runner-images:ubuntu-latest'
gitea_runner config set container.network gitea-runner_default
```

The last line puts build jobs on the runner's own Docker network, so they can reach the runner's cache server. Docker Compose names that network after the folder, `gitea-runner`.

On the backup runner, use `30s` for both fetch settings. Afterwards, check the file:

```bash
ls -l config.yaml && cat config.yaml
```

The `ls` line must start with `-rw` (a file), not `d` (a folder). **Don't start the runner until `config.yaml` exists.** If Docker starts the runner first, it creates an empty folder called `config.yaml`, and the runner fails with "is a directory". If that happens, stop the service, run `sudo rm -rf config.yaml`, and repeat this step.

To see every option with an explanation, run `gitea_runner config generate | less`.

## 4. Create the `env` file

```bash
micro env
```

```
CONFIG_FILE=/config.yaml
GITEA_INSTANCE_URL=https://git.example.com
GITEA_RUNNER_REGISTRATION_TOKEN=paste-token-here
GITEA_RUNNER_NAME=main
```

The token is only used the first time the runner starts. After that, the registration is saved in `data/.runner`. Keep the file private:

```bash
chmod 600 env
```

## 5. Create `docker-compose.yml`

```bash
micro docker-compose.yml
```

```yaml
services:
  runner:
    image: docker.io/gitea/runner:3.5.0
    restart: unless-stopped
    env_file: env
    volumes:
      - ./config.yaml:/config.yaml
      - ./data:/data
      - /var/run/docker.sock:/var/run/docker.sock
    logging:
      driver: journald
```

The runner has no web page, so it needs no ports.

## 6. Create the systemd service

Put your user name in place of `YOUR_USER`. `whoami` prints it.

```bash
sudo micro /etc/systemd/system/gitea-runner.service
```

```ini
[Unit]
Description=Gitea Actions runner
Requires=docker.service
After=docker.service network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=/home/YOUR_USER/Applications/gitea-runner
ExecStart=/usr/bin/docker compose up -d
ExecStop=/usr/bin/docker compose down

[Install]
WantedBy=multi-user.target
```

Then run:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now gitea-runner.service
```

## 7. Check it worked

```bash
systemctl status gitea-runner.service
```

```bash
journalctl CONTAINER_NAME=gitea-runner-runner-1 -n 30
```

The log should show that the runner registered with its labels. It should also show as **Idle** on the Runners page where you got the token.

If you change `config.yaml` later, restart the runner:

```bash
sudo systemctl restart gitea-runner.service
```
