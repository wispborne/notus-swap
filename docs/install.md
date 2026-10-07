# Installing notus-swap

This puts notus-swap in front of a llama-swap that is already running:

- **Before:** clients → `server:8080` (llama-swap)
- **After:** clients → `server:8080` (notus-swap) → `127.0.0.1:8081` (llama-swap)

Clients keep using the same address, because notus-swap takes over port 8080. llama-swap moves to a port that only the server itself can reach. So a reverse proxy or firewall in front of the server needs no changes.

Clients lose access only for the few seconds in step 5, while llama-swap restarts on its new port.

The guide assumes this setup. Change the commands to match yours:

- llama-swap runs as the systemd service `llama-swap.service`, as your own user, and listens on port 8080.
- llama-swap's files are in `~/Applications/llama-swap/`: the `llama-swap` binary and `config.yaml`.
- notus-swap goes in `~/Applications/notus-swap/`.

Any folders work. These are only examples.

Wherever the guide shows `YOUR_USER`, put your user name. `whoami` prints it. Run everything on the server.

Everything specific to your machine goes in notus-swap's `env` file (step 1), not in the code.

## 1. Make the folder and the `env` file

```bash
mkdir -p ~/Applications/notus-swap && cd ~/Applications/notus-swap
```

```bash
micro env
```

```
NOTUS_LISTEN=:8080
NOTUS_UPSTREAM=http://127.0.0.1:8081
NOTUS_DB=/home/YOUR_USER/Applications/notus-swap/notus-swap.db
NOTUS_LLAMA_SWAP_CONFIG=/home/YOUR_USER/Applications/llama-swap/config.yaml
NOTUS_LLAMA_SWAP_BIN=/home/YOUR_USER/Applications/llama-swap/llama-swap
```

```bash
chmod 600 env
```

The last two lines turn on the Model Config page and llama-swap updates on the System page. The Model Config page edits llama-swap's `config.yaml`, and checks each change with `llama-swap -validate` before saving. `-validate` only exists in recent llama-swap releases. With an older llama-swap, the page still checks the YAML, and lets you save without llama-swap's check. Updating llama-swap from the System page adds `-validate`.

`NOTUS_LISTEN=:8080` listens on every network interface, the same as a llama-swap listening on `0.0.0.0:8080`. So it relies on the same firewall rules that already limit who can reach port 8080. notus-swap has no login, so never let the open internet reach this port.

Once the socket unit from step 4 is running, it sets the port instead, and `NOTUS_LISTEN` is ignored. Keep the line anyway, for running notus-swap by hand.

The README lists every setting `env` can hold.

## 2. Get the binary

Download the newest release from GitHub, along with its checksum:

```bash
cd ~/Applications/notus-swap && for f in notus-swap-linux-amd64 notus-swap-linux-amd64.sha256; do curl -fLO "https://github.com/wispborne/notus-swap/releases/latest/download/$f"; done
```

Verify the checksum, then name the binary and make it executable:

```bash
sha256sum -c notus-swap-linux-amd64.sha256 && mv notus-swap-linux-amd64 notus-swap && chmod +x notus-swap
```

`sha256sum` must print `notus-swap-linux-amd64: OK`.

**To build it yourself instead,** follow the README's Building section. Then copy the binary to the server, from the machine you built it on:

```bash
scp notus-swap YOUR_USER@server:Applications/notus-swap/
```

Then, on the server:

```bash
chmod +x ~/Applications/notus-swap/notus-swap
```

## 3. Try it without changing anything

This runs notus-swap on a spare port, in front of llama-swap as it is today. It uses a throwaway database.

```bash
./notus-swap -listen 127.0.0.1:18080 -upstream http://127.0.0.1:8080 -db /tmp/notus-swap-test.db
```

It should print `notus-swap started` with the version. Leave it running, then open a **second** SSH session to the server and run:

```bash
curl -s http://127.0.0.1:18080/notus/api/health
```

```bash
curl -s http://127.0.0.1:18080/v1/models | head -c 300; echo
```

The first command should print `{"ok":true,...}`. The second should print the start of your model list, fetched from llama-swap through notus-swap. Then stop notus-swap with Ctrl-C in the first session and delete the test database:

```bash
rm -f /tmp/notus-swap-test.db*
```

## 4. Create the systemd socket and service

systemd holds notus-swap's port open, and hands it to notus-swap when it starts. The port then stays open while notus-swap restarts. Requests that arrive during a restart wait a moment instead of being refused.

```bash
sudo micro /etc/systemd/system/notus-swap.socket
```

```ini
[Unit]
Description=notus-swap port

[Socket]
ListenStream=8080

[Install]
WantedBy=sockets.target
```

`ListenStream=8080` listens on every network interface, like `NOTUS_LISTEN=:8080`. Use `ListenStream=127.0.0.1:8080` for this machine only.

```bash
sudo micro /etc/systemd/system/notus-swap.service
```

```ini
[Unit]
Description=notus-swap
Requires=notus-swap.socket
After=notus-swap.socket network-online.target llama-swap.service
Wants=network-online.target llama-swap.service

[Service]
Type=simple
User=YOUR_USER
WorkingDirectory=/home/YOUR_USER/Applications/notus-swap
EnvironmentFile=/home/YOUR_USER/Applications/notus-swap/env
ExecStart=/home/YOUR_USER/Applications/notus-swap/notus-swap
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
```

Three details:

- `Wants=` starts llama-swap along with notus-swap. It doesn't stop notus-swap when llama-swap stops. While llama-swap is down, notus-swap stays up and answers with a 503.
- Self-update replaces the binary and exits; systemd's `Restart=always` starts the new version.
- Planned restarts (updates, roll backs, and the Restart button) wait until no requests are in flight, so no answer is cut off. The System page shows how many it is waiting for, and has a **Restart now** button.

```bash
sudo systemctl daemon-reload
```

Don't start it yet.

## 5. Switch over

Change llama-swap's listen address from `0.0.0.0:8080` (or `:8080`) to `127.0.0.1:8081`. Where that is depends on how you start llama-swap: the `-listen` flag in `llama-swap.service`'s `ExecStart` line, or in a script that the service runs. To find it:

```bash
systemctl cat llama-swap.service
```

Keep a copy of whichever file you change, then edit it with `micro`. For example, if the unit runs a script called `start-llama-swap.sh`:

```bash
cd ~/Applications/llama-swap && cp start-llama-swap.sh start-llama-swap.sh.before-notus-swap
```

```bash
micro start-llama-swap.sh
```

If you changed the unit file itself instead, run `sudo systemctl daemon-reload` afterwards.

Now restart llama-swap on its new port and start notus-swap on 8080:

```bash
sudo systemctl restart llama-swap.service && sudo systemctl enable --now notus-swap.socket notus-swap.service
```

## 6. Check it worked

```bash
systemctl status notus-swap.service llama-swap.service
```

```bash
journalctl -u notus-swap.service -n 20
```

```bash
ss -ltnp | grep -E ':(8080|8081) '
```

You should see notus-swap on `*:8080` and llama-swap on `127.0.0.1:8081`. Then check the whole path from another machine, through your reverse proxy if you have one:

```bash
curl -s https://llm.example.com/v1/models | head -c 300; echo
```

Finally, send a chat message from any client, and look at what notus-swap recorded. This needs `sqlite3` (`sudo apt install sqlite3`):

```bash
sqlite3 -header -column ~/Applications/notus-swap/notus-swap.db "SELECT id, datetime(started_at/1000,'unixepoch','localtime') AS started, model, state, status_code, completion_tokens, round(predicted_per_second,1) AS tok_s, (first_token_at-started_at) AS ttft_ms FROM requests ORDER BY id DESC LIMIT 5"
```

llama-swap's own UI still works at `https://llm.example.com/ui`, through notus-swap.

## 7. Let notus-swap restart services

The llama-swap service buttons and updates need permission to control that service without a password. This polkit rule allows `YOUR_USER` to start, stop, and restart only `llama-swap.service` and `notus-swap.service`:

```bash
sudo micro /etc/polkit-1/rules.d/50-notus-swap.rules
```

```js
polkit.addRule(function (action, subject) {
  if (action.id == "org.freedesktop.systemd1.manage-units" && subject.user == "YOUR_USER") {
    var unit = action.lookup("unit");
    var verb = action.lookup("verb");
    if ((unit == "llama-swap.service" || unit == "notus-swap.service") &&
        (verb == "start" || verb == "stop" || verb == "restart")) {
      return polkit.Result.YES;
    }
  }
});
```

polkit loads the rule automatically. To test it, restart llama-swap **without** `sudo`:

```bash
systemctl restart llama-swap.service && echo restarted
```

It should print `restarted` without asking for a password. If prompted, press Ctrl-C and check `/etc/polkit-1/rules.d/50-notus-swap.rules`.

If llama-swap's unit has another name, put it in this rule and in `env` as `NOTUS_LLAMA_SWAP_UNIT`.

## 8. Let notus-swap update llama.cpp (optional)

The System page can install new llama.cpp releases from GitHub. It works with this layout:

- Each build gets its own folder, named like the download: `llama-b11146-bin-ubuntu-rocm-10.0-x64`.
- A link called `current` points at the build in use.
- llama-swap's `config.yaml` starts models from `current`, such as `/home/YOUR_USER/Applications/llama.cpp/current/llama-server`. So a model uses a new build the next time it loads, with no config change.

An update downloads the build, checks its SHA-256, unpacks it, and points `current` at it. The build that was in use is kept, so the Roll back button can switch to it. Older builds are deleted, unless a program is still running from one.

If you already keep llama.cpp this way, add its folder to `env`:

```bash
micro ~/Applications/notus-swap/env
```

```
NOTUS_LLAMA_CPP_DIR=/home/YOUR_USER/Applications/llama.cpp
NOTUS_LLAMA_CPP_FLAVOR=ubuntu-rocm-10.0-x64
```

The flavor is the part of the download's name after `-bin-`. It depends on your GPU: `ubuntu-rocm-10.0-x64` for AMD with ROCm, `ubuntu-vulkan-x64` for Vulkan, `ubuntu-cuda-13.4-x64` for NVIDIA, or `ubuntu-x64` for CPU only. The list is on any build's page at https://github.com/ggml-org/llama.cpp/releases. When ROCm or CUDA moves to a new version, the name changes, and an update says which names the release has. Without this line, notus-swap uses the flavor of the build that `current` points at.

The prebuilt ROCm and CUDA builds don't include the GPU driver stack. That must already be installed.

llama.cpp publishes a weekly release, such as `v0.5.0`, and several nightly builds a day, such as `b11185`. A weekly release is made from one nightly build. The System page offers both, and its update dot only counts weekly releases.

Restart notus-swap to pick up the change:

```bash
sudo systemctl restart notus-swap.service
```

notus-swap checks GitHub for llama-swap and llama.cpp once an hour, using 3 requests, and for its own releases every 15 minutes, using 1. That is 7 requests an hour. GitHub allows 60 an hour from each IP address without a token. If other machines on your network use GitHub's API too, you can add a GitHub token to `env` as `GITHUB_TOKEN`. It needs no permissions, because the releases are public.

## 9. Let notus-swap read CPU power (optional)

The status bar's "system ≈ … W" estimate needs the CPU's power. On some AMD Ryzen CPUs, the built-in graphics has a power sensor labelled `PPT` that covers the whole CPU socket, and notus-swap uses that when nothing better is available. If the status bar already shows a system estimate, you can skip this step.

The CPU's own energy counter (RAPL) is the other source, and notus-swap prefers it when it can read it. Linux lets only root read it. This udev rule lets your user's group read it too.

First check that the reading exists. The command should print `package-0`:

```bash
cat /sys/class/powercap/intel-rapl:0/name
```

```bash
sudo micro /etc/udev/rules.d/60-notus-swap-rapl.rules
```

```
# Let YOUR_USER's group read CPU energy counters, for notus-swap's system power estimate.
SUBSYSTEM=="powercap", KERNEL=="intel-rapl:*", RUN+="/bin/chgrp YOUR_USER /sys%p/energy_uj", RUN+="/bin/chmod g+r /sys%p/energy_uj"
```

Apply it now. It also applies by itself at every boot:

```bash
sudo udevadm control --reload && sudo udevadm trigger --subsystem-match=powercap --action=add
```

Check that you can read it without `sudo`. It should print a large number:

```bash
cat /sys/class/powercap/intel-rapl:0/energy_uj
```

notus-swap picks up the reading within a second. No restart is needed.

The estimate is `(GPUs + CPU + base) / efficiency`:

- **base** stands for everything else: the motherboard, RAM, drives, fans, and so on. The default is 35 W.
- **efficiency** is the power supply's efficiency. The default is 0.90.

To set them for your machine, add these lines to `env` and restart notus-swap:

```
NOTUS_SYSTEM_BASE_WATTS=35
NOTUS_PSU_EFFICIENCY=0.90
```

Energy counters can reveal a little about what other programs are doing, which is why Linux hides them by default. On a single-user server, letting your own user read them is fine.

## Update to the newest release

On the **System** page, press **Update** to install the newest release. notus-swap checks its checksum, keeps the current binary as `notus-swap.prev`, and restarts. If the new version stops before 30 seconds twice, it restores the previous version automatically. Use **Roll back** to restore the previous version yourself.

Releases come from GitHub. notus-swap checks for new ones every 15 minutes. If other machines on your network use GitHub's API too, add a `GITHUB_TOKEN` to `env`, as step 8 describes. To turn notus-swap's updates off, add an empty `NOTUS_GITHUB_REPO=` line to `env`.

Use the script below when the web UI is unavailable.

`scripts/update-notus-swap.sh` updates with one command:

1. It checks the newest release against the running version.
2. It downloads the new binary and checks its checksum.
3. It keeps the running binary as `notus-swap.prev`.
4. It restarts the service and waits for the new version to answer.

If the new version doesn't respond within 30 seconds, the script restores the previous one. It reads the same `env` file as notus-swap.

Download the script once:

```bash
cd ~/Applications/notus-swap && curl -fLO https://raw.githubusercontent.com/wispborne/notus-swap/main/scripts/update-notus-swap.sh && chmod +x update-notus-swap.sh
```

To update:

```bash
~/Applications/notus-swap/update-notus-swap.sh
```

To roll back:

```bash
~/Applications/notus-swap/update-notus-swap.sh --rollback
```

`--force` reinstalls the newest release even if it's already running. Run the download command again whenever the script itself changes.

## Getting updates from your own Gitea

If you keep a copy of the repo on your own Gitea server, it can build the releases instead of GitHub. Gitea runs `.gitea/workflows/release.yml` on every push to `main`. It tests the code, builds the binary, and publishes it as a Gitea release. [runner-setup.md](runner-setup.md) shows how to set up the Gitea runners it needs.

To make notus-swap update from there, first create a token. In Gitea, go to your avatar → Settings → Applications → Generate New Token:

- **Name:** `notus-swap updater`
- **Permissions:** Repository: **Read**. Leave everything else as "No access".

Copy the token. Gitea only shows it once. Then add these lines to `env`:

```bash
micro ~/Applications/notus-swap/env
```

```
GITEA_URL=https://git.example.com
GITEA_REPO=owner/notus-swap
GITEA_USER=owner
GITEA_TOKEN=paste-token-here
```

Restart notus-swap to pick up the change:

```bash
sudo systemctl restart notus-swap.service
```

When all four lines are set, the update button and `update-notus-swap.sh` use Gitea instead of GitHub. To download the script from Gitea:

```bash
cd ~/Applications/notus-swap && set -a; . ./env; set +a && curl -sSf -u "$GITEA_USER:$GITEA_TOKEN" -o update-notus-swap.sh "$GITEA_URL/api/v1/repos/$GITEA_REPO/raw/scripts/update-notus-swap.sh?ref=main" && chmod +x update-notus-swap.sh
```

## Undo everything

To go back to llama-swap on its own, stop notus-swap:

```bash
sudo systemctl disable --now notus-swap.socket notus-swap.service
```

Stop both. While the socket runs, the next request to port 8080 starts notus-swap again.

Then put back the copy of llama-swap's start script or unit from step 5, and restart llama-swap. With the example from step 5:

```bash
cp ~/Applications/llama-swap/start-llama-swap.sh.before-notus-swap ~/Applications/llama-swap/start-llama-swap.sh && sudo systemctl restart llama-swap.service
```

You can leave the notus-swap folder, database, and polkit rule in place.
