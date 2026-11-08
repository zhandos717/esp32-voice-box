# Contributing

Thanks for stopping by! Bug reports, ideas and pull requests are welcome, in English or Russian.

## Before a pull request

1. Branch off `main`.
2. Check the server:
   ```bash
   cd server
   go vet ./...
   go test ./...
   ```
3. If you changed the firmware, build it:
   ```bash
   arduino-cli compile --fqbn esp32:esp32:esp32 firmware/mic_stream
   ```
4. If you changed the board ↔ server protocol, update [docs/protocol.md](docs/protocol.md). Describe new `.env` variables in
   `.env.example` and the README.

## Code style

- The server is Go with no external dependencies. Add one only when there is no reasonable way around it, and say why in the PR.
- The control panel (`server/web/index.html`) has no build step: Vue is a bundled file. Add UI strings in all three languages
  (`ru`, `kk`, `en`).
- Comment only what the code cannot say by itself: the "why".
- Never commit `.env`, tokens, Wi-Fi passwords or recordings.

## Reporting bugs

Tell us the board, the firmware version (control panel → "Board" tab), what you did and what happened. Server logs:
`journalctl -u esp32-voice-box -n 100`. Remove tokens and addresses from logs.
