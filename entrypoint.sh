#!/usr/bin/env bash
# Two modes, and the difference matters.
#
#   init  -- ONE TIME per volume. Creates the gpg key and the `pass` store,
#            then drops into Bridge's interactive CLI so a human can `login`
#            with a password and a 2FA code. Running it again on a volume that
#            already has a store generates another key on top of the first.
#
#   (none) -- the daemon. `proton-bridge -n` is upstream's own non-interactive
#            mode (their Makefile's `run-noninteractive` target passes -n),
#            so there is no console attached and nothing to type into.
#
# CAPTURE `info` DURING `init`. The daemon has no CLI, and starting a second
# `proton-bridge -c` against the same `pass` store would be a second Bridge
# instance on one state directory. The generated IMAP password is printed once,
# in that session, and it is not the Proton account password.
set -euo pipefail

STORE_DIR="${GNUPGHOME:-/root/.gnupg}"
PASS_DIR="${PASSWORD_STORE_DIR:-/root/.password-store}"

init_store() {
    if [[ -d "$PASS_DIR" ]]; then
        echo "entrypoint: a pass store already exists at $PASS_DIR -- not re-initialising." >&2
        echo "entrypoint: to log in again use the CLI's \`login\`, not \`init\`." >&2
        return 0
    fi
    echo "entrypoint: creating the gpg key and pass store (once per volume)" >&2
    gpg --batch --passphrase '' --quick-generate-key "proton-bridge (local keychain)" default default never
    local fpr
    fpr=$(gpg --list-secret-keys --with-colons | awk -F: '/^fpr:/ {print $10; exit}')
    pass init "$fpr"
}

case "${1:-}" in
init)
    init_store
    # -c is the interactive CLI. `login`, then `info`, then `exit`.
    exec proton-bridge -c
    ;;
cli)
    # Escape hatch for debugging an existing store. Do NOT run this while the
    # daemon is up: two Bridge processes on one state directory is not a
    # supported configuration.
    exec proton-bridge -c
    ;;
*)
    if [[ ! -d "$PASS_DIR" ]]; then
        echo "entrypoint: no pass store at $PASS_DIR -- run the image with \`init\` first." >&2
        exit 1
    fi
    # Bridge binds IMAP and SMTP on loopback and expects its clients to appear
    # to come from there. socat bridges the published ports to those listeners,
    # which is also what lets the container expose the standard numbers.
    #
    # Publish these to 127.0.0.1 on a workstation, or keep them on a ClusterIP
    # with a NetworkPolicy in a cluster. They carry decrypted mail.
    socat TCP-LISTEN:143,fork,reuseaddr TCP:127.0.0.1:1143 &
    socat TCP-LISTEN:25,fork,reuseaddr  TCP:127.0.0.1:1025 &
    exec proton-bridge -n
    ;;
esac
