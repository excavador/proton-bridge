# proton-bridge (headless container image)

Proton Mail Bridge, built from Proton's own source with their `build-nogui`
target, as a current multi-arch image for headless and Kubernetes use.

```
ghcr.io/excavador/proton-bridge:3.27.0
```

`linux/amd64` and `linux/arm64`.

## Why this exists

Proton publishes **no** container image and does not support Docker or
Kubernetes deployment — Bridge is shipped as a desktop application. Every
containerised Bridge is therefore a repack, and the choice is only *whose*.

The most-used community image was **eight releases behind upstream** when this
repository was written, and its `latest` tag is **amd64-only**, which rules it
out of an arm64 cluster entirely. Bridge decrypts every message in the mailbox
and holds the Proton session, so being current matters more here than for most
dependencies.

What this is not: a fork. Bridge is not patched and not vendored. The image
builds one pinned upstream tag with a target Proton maintains —
`build-nogui` is declared in their Makefile's own `.PHONY` list — and
Renovate watches that tag so falling behind becomes visible instead of silent.

## Running it

Two modes, and the difference matters.

### One time per volume: the login

```bash
docker volume create protonmail
docker run --rm -it -v protonmail:/root ghcr.io/excavador/proton-bridge:3.27.0 init
```

That creates the GPG key and `pass` store, then drops into Bridge's
interactive CLI. There:

1. `login` — your Proton address, password, then the 2FA code.
2. `info` — prints the **Bridge-generated** IMAP/SMTP username and password.
   This is Bridge's own credential, not your Proton password, and it is what
   every local client uses from now on.
3. `exit`.

**Copy the `info` output before you exit.** The daemon runs
`proton-bridge -n`, upstream's non-interactive mode, so it has no console to
type into afterwards — and starting a second `proton-bridge -c` against the
same `pass` store would put two Bridge processes on one state directory.

Run `init` **once per volume**. On a volume that already has a store it
refuses rather than stacking a second GPG key on the first. To log in again,
use the CLI's own `login` (`docker run … cli`), not `init`.

### Then: the daemon

```bash
docker run -d --name protonmail-bridge \
  -v protonmail:/root \
  -p 127.0.0.1:1143:143 \
  -p 127.0.0.1:1025:25 \
  --restart unless-stopped \
  ghcr.io/excavador/proton-bridge:3.27.0
```

**Bind to `127.0.0.1` explicitly.** Without the address, Docker publishes on
every interface and installs a DNAT rule that bypasses the host firewall —
putting a decrypted view of the mailbox on the local network. Check, don't
assume:

```bash
ss -lntp | grep -E '1143|1025'   # must say 127.0.0.1, never 0.0.0.0 or *
```

Bridge presents a self-signed certificate on IMAP. Disabling verification is
defensible on a loopback hop and nowhere else.

## In Kubernetes

- **Persist `/root`.** It holds the `pass`/GPG keychain, the Proton session
  and the message cache. Lose it and you are back at a 2FA prompt — and a
  fresh login mints *new* local IMAP credentials, so it costs a secrets update
  too. Use replicated storage if nodes are replaced or drained routinely;
  node-local storage pins the pod and turns ordinary maintenance into a manual
  ceremony.
- **`strategy: Recreate`**, not `RollingUpdate`. The volume is `ReadWriteOnce`,
  so a rolling pod waits forever on a volume the outgoing pod still holds.
- **ClusterIP and a NetworkPolicy.** Port 143 carries decrypted mail; its
  reachability is the containment boundary.
- The login is `kubectl exec -it` into the pod with the command overridden to
  something inert, so the volume is mounted and nothing is running against it.

## Ports and the keychain

The container exposes the standard **143** and **25**. Bridge itself binds
`1143`/`1025` on loopback and expects clients to appear to come from there, so
the entrypoint runs two `socat` forwarders. That is a property of Bridge, not
a preference of this image.

For credentials Bridge probes for a Linux keychain and **prefers `pass`** when
it finds one — see `pkg/keychain/helper_linux.go` upstream, which looks `pass`
up on `PATH` and makes it the default helper, falling back to secret-service
only when it is absent. So `pass` plus `gnupg` here is upstream's own
preference rather than a container workaround. `libsecret` is installed anyway,
in both stages, because the secret-service backend is compiled in
unconditionally and the build fails without its headers.

## Licence

Proton Mail Bridge is **GPL-3.0**, and so is this repository: the published
image contains GPLv3 binaries. The corresponding source is the upstream tag
named in `ARG BRIDGE_VERSION` at the top of the `Dockerfile`, unmodified.

Not affiliated with or endorsed by Proton AG.
