# Security

Arkey v3 is a public source repository. Never commit API keys, provider tokens,
Codex, Claude, or Kimi state, conversation history, locally snapshotted client
executables, local configuration, model weights, GPU state, logs, or generated
runtime databases.

The committed MoonBridge file is a credential-free example. The installer
copies it to `~/.config/arkey/moonbridge.yml` with mode `0600`; users configure
their credentials only in that ignored, machine-local file.

Report a vulnerability privately through GitHub's security-advisory interface
for this repository. Do not include live credentials in an issue, log, test, or
reproduction.
