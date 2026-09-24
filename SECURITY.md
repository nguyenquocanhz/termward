# Security

Termward holds SSH keys and passwords for your servers, so security reports
are taken seriously.

## Reporting a vulnerability

Please **do not open a public issue**. Use GitHub's private
[security advisory form](https://github.com/nguyenquocanhz/termward/security/advisories/new)
instead. You will get a reply within a few days.

## How Termward protects your data

- **Private keys** are stored in the app data directory with `0600`
  permissions and never leave the device. Only public keys are ever sent to a
  server (when you choose "Deploy to server").
- **Passwords and key passphrases** are kept in memory for the session. When
  you tick "Remember", they go to the OS keychain (Windows Credential Manager,
  macOS Keychain, Secret Service on Linux). On Android and iOS they are stored
  AES-256-GCM encrypted, with the key held by the Android Keystore / iOS
  Keychain (this device only, never synced).
- **Host keys** are verified on every connection. A new server shows its
  fingerprint and must be trusted explicitly; a changed key is blocked with a
  warning, never silently accepted. Keys you already trust in
  `~/.ssh/known_hosts` are honored.
- **The local API** between the UI and the Go core listens on `127.0.0.1`
  only and requires a random 256-bit token generated at every start.
- **The desktop UI** runs sandboxed with context isolation, no Node.js
  integration and a strict Content Security Policy.
- **Mobile backups** exclude Termward's data (keys, hosts, secrets).
- **No telemetry.** Termward only talks to the servers you add.
