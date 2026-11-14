# Mod pack launcher

Opening the program with no arguments shows the pack window. Each pack is one row. Its page has a version menu. Play installs that version, copies missing saved ships and game saves into `ModPackProfile`, enables and trusts every mod in that profile's `settings.rules`, and leaves the pack installed after Cosmoteer closes. A file already in the mod profile is left unchanged. Uninstall puts Standard Mods and the Bin proxy back. The Steam profile's settings are not changed.

Pack updates come from the public repository `kyvoracloud-klyne/cosmoteer-modpack-2026`. Each release includes `manifest.json` and the archive it names. When a release also lists `files` and `deltas`, and an older pack version is already cached, the launcher downloads only the delta zip and applies it instead of the multi-gigabyte full archive. Old launchers ignore those fields and still use the full archive. Launcher updates come from this repository. The release assets are `modpack-launcher-linux-amd64` and `modpack-launcher-windows-amd64.exe`.

This repository is public. Launcher and pack updates do not need a token.
