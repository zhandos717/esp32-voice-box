# Third-party code

| What | Where | License |
|---|---|---|
| [Vue.js](https://github.com/vuejs/core) 3.5.13 | `server/web/vue.js` | MIT, © Yuxi (Evan) You and Vue contributors |
| [libopus](https://opus-codec.org/) via [codec-opus](https://github.com/pschatzmann/codec-opus) | fetched by `scripts/get-libs.sh` into `firmware/libraries`, linked into the firmware | BSD-3-Clause, © Xiph.Org, Skype, Microsoft and others |
| [Telegram Web App script](https://core.telegram.org/bots/webapps) | loaded from telegram.org by the page | Telegram terms |

Programs the server calls but does not include: ffmpeg (LGPL/GPL), edge-tts (LGPL-3.0, an unofficial client of a Microsoft
service, for personal non-commercial use only), Piper (MIT). Azure Speech voices are subject to Microsoft's terms.
