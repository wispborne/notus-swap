# Running notus-swap without systemd (Windows, macOS)

The main [install guide](install.md) is for Linux with systemd. On Windows and macOS, and on Linux without systemd, notus-swap runs llama-swap itself and restarts itself. This guide covers that setup.

## How it works

- **llama-swap.** When notus-swap starts, it starts llama-swap on the address in `-upstream` (`127.0.0.1:8081` by default), with `-config`, `-listen` and `-watch-config`. If a llama-swap started from the same binary is already running, notus-swap uses that one instead.
- **Stopping.** Stopping notus-swap with Ctrl-C, or by closing its window, stops llama-swap and the models it loaded.
- **Restarting.** The Restart button, an update and a roll back restart notus-swap but leave llama-swap running, so loaded models stay loaded. When notus-swap starts, it starts a copy of itself and waits. When that copy asks for a restart, notus-swap starts it again. This does the job that systemd's `Restart=always` does on Linux.
- **The Settings page's llama-swap buttons** (Start, Stop, Restart) start and stop that llama-swap program. On Windows, stopping llama-swap ends it and its model servers at once, because Windows has no gentle way to ask a console program to stop. Model servers keep no state, so nothing is lost.

## What is measured on each system

| | Linux | Windows | macOS |
| --- | --- | --- | --- |
| NVIDIA GPUs (through `nvidia-smi`) | yes | yes | no |
| AMD GPUs | yes | no | no |
| CPU power and the system power estimate | yes | no | no |
| RAM | yes | yes | no |

Requests, speeds, the config editor and updates work on all three. Energy per request needs a GPU power reading.

## Install on Windows

These steps assume llama-swap is in `C:\llama-swap`, with `llama-swap.exe` and `config.yaml`. Any folder works.

1. Download `notus-swap-windows-amd64.exe` from the [latest release](https://github.com/wispborne/notus-swap/releases/latest). Save it in `C:\llama-swap` as `notus-swap.exe`.
2. Stop llama-swap if it is running. notus-swap takes over its port, 8080, and starts llama-swap on 8081.
3. Make `C:\llama-swap\start-notus-swap.bat`:

   ```bat
   @echo off
   cd /d "%~dp0"
   notus-swap.exe -listen 0.0.0.0:8080 -db "%~dp0notus-swap.db" -llama-swap-config "%~dp0config.yaml"
   pause
   ```

4. Run it. Windows may ask whether notus-swap can use the network. Allow it on private networks only.
5. Open http://localhost:8080/notus/.

Clients keep using port 8080. llama-swap's own UI is still at http://localhost:8080/ui/.

To set other settings, add `set` lines before the `notus-swap.exe` line, such as `set NOTUS_LLAMA_CPP_DIR=C:\llama.cpp`. The settings are listed in the [README](../README.md#running).

## Let notus-swap update llama.cpp on Windows

This works as on Linux (see step 8 of the [install guide](install.md)), with these differences:

- `current` is a junction, which is Windows' kind of folder link. Making one needs no admin rights. If you already have a `current` junction, notus-swap uses it. To make one by hand: `mklink /J current llama-b11146-bin-win-cuda-12.4-x64`.
- Flavors start with `win-`, such as `win-cuda-12.4-x64`, `win-vulkan-x64`, `win-rocm-10.0-x64` or `win-cpu-x64`.
- CUDA builds need the CUDA runtime. notus-swap downloads it too and puts its files in the build's folder.
- llama-swap's `config.yaml` starts models from `current`, such as `C:/llama.cpp/current/llama-server.exe`.

## Show this server on another notus-swap

Another notus-swap can show this one, added on its Settings page under Servers. That server calls this one's port directly, so Windows Firewall must let it in. In PowerShell as administrator, with the other server's address:

```powershell
New-NetFirewallRule -DisplayName "notus-swap" -Direction Inbound -Protocol TCP -LocalPort 8080 -RemoteAddress 192.168.1.10 -Action Allow
```

Limiting the rule to that one address means only that server can reach this one. Everyone else reaches it through that server.

Give this computer an address that doesn't change, such as a DHCP reservation on your router or a name in your local DNS. If the address does change, edit it under Servers on the other server.

A computer that sleeps shows as "Asleep or off" on the other server, and its pages come back by themselves once it wakes.

## Settings for this setup

| Flag | Env variable | Default | What it does |
| --- | --- | --- | --- |
| `-llama-swap-service` | `NOTUS_LLAMA_SWAP_SERVICE` | `systemd` on Linux, `process` elsewhere | `process`: notus-swap runs llama-swap. `systemd`: systemd runs it. `none`: notus-swap doesn't start or stop it. |
| `-llama-swap-args` | `NOTUS_LLAMA_SWAP_ARGS` | `-watch-config` | More arguments for llama-swap, after `-config` and `-listen`. |
| | `NOTUS_SUPERVISE` | on | Set to `0` when something else restarts notus-swap, such as a service manager. Then the Restart button and updates only stop notus-swap. |

`process` needs `-llama-swap-config`. The binary is looked for next to the config unless `-llama-swap-bin` is set.
