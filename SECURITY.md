# Security

If you find a vulnerability (bypassing the control panel login, access to recordings, firmware tampering), please do not open
a public issue. Report it privately through [GitHub Security Advisories](../../security/advisories/new).

What is protected and how:

- The board logs in to the server with the `MIC_TOKEN` password; a wrong attempt costs 2 seconds.
- The control panel opens only from Telegram: `initData` is verified with the bot token and only `MIC_WEB_USERS` get in;
  the session is a signed cookie, requests from another Origin are refused.
- OTA updates: the board checks the image and rolls back to the previous firmware if the new one does not reach the server.

The board protocol is plain TCP without encryption: the password and audio can be sniffed on the network. Do not expose
port 8800 to networks you do not trust.
