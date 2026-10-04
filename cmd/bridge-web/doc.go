// Command bridge-web is the small web face of a headless Proton Mail Bridge.
//
// It has exactly two modes and switches between them on observable state,
// never on a flag:
//
//   - NO ACCOUNT YET -- serves a two-field form (password, 2FA code) and, on
//     submit, drives Bridge's own CLI to log in. This is reachable only while
//     there is no account, which matters: the dangerous capability is gated on
//     state, not merely on authentication. An attacker who reaches the
//     endpoint with a healthy vault gets diagnostics and no way to ask for
//     login mode.
//
//   - LOGGED IN -- serves diagnostics derived from Bridge's log files on the
//     shared volume: sync progress, a live tail, the account name. Reading
//     files cannot contend with the running daemon, which is why this mode
//     does not shell out at all.
//
// WHY THE MODES CANNOT OVERLAP. Bridge is single-instance: it takes a lock on
// its state directory, and a second process fails with "another instance is
// already running". The daemon is only useful once logged in, so the login
// path runs exactly when no daemon holds the lock. The sibling container
// waits for the marker this writes before starting the daemon.
//
// It never renders a credential. Bridge's generated IMAP password belongs in
// OpenBao, and a diagnostics page that displays secrets is one you cannot
// safely leave exposed.
package main
