# Speed Limiter for Google Chrome™

A configurable, shared downstream bandwidth limiter for Google Chrome on Windows, by **Unwritten Byte**.

Speed Limiter uses a local, authenticated proxy and a Chrome extension to pace eligible HTTP and encrypted HTTPS traffic. It is free software, with no accounts, subscriptions or developer-operated traffic relay.

## Download and installation

- **[Add to Chrome](https://chromewebstore.google.com/detail/speed-limiter-for-google/agbejnpaacdhklaocalpballabcibmgm)** — extension v1.0.6.3.
- **[Download and setup guide](https://unwrittenbyte.com/speed-limiter.html#download)** — free Windows companion v1.0.6.

Both components are required. Follow the extension's setup instructions, close Chrome completely before running the companion installer, and reopen Chrome afterward.

The Windows companion installer is unsigned. Keep Windows security protections enabled; optional checksum verification instructions are available on the official website.

**Source status:** The extension source in this repository matches the published Chrome Web Store package v1.0.6.3. The Windows companion remains v1.0.6.

## Features

- Configurable speed cap shared across proxied connections, in KB/s, MB/s, GB/s or Mbps.
- Change the cap or switch limiting on/off during an ongoing proxied download.
- Disconnect the local proxy separately to restore prior proxy settings.
- Live aggregate traffic, connection status and local diagnostic counters.
- Optional, experimental VPN compatibility for specified synthetic DNS mappings.

## Requirements and limitations

Windows 10 or 11 (x64); Chrome 120 or newer; both the extension and companion are required. The cap applies to eligible proxied downstream traffic, not every Chrome connection or other applications. Existing direct connections may bypass the proxy until restarted. Local destinations, incognito, UDP/WebRTC and some browser services are outside its scope. Chrome may fall back to a direct connection if the local proxy is unavailable. This utility is not a firewall or VPN. Do not rely on it to enforce VPN routing or a security boundary. VPN compatibility is not universal. KB/MB/GB use binary multiples; Mbps uses decimal units.

## Source layout

| Path | Purpose |
| --- | --- |
| `extension/` | Manifest V3 extension, popup and setup page |
| `host/` | Go native messaging host and authenticated loopback proxy |
| `installer/` | Per-user Windows installer and uninstaller source |
| `tests/` | Mocked browser tests and native protocol checks |
| `Build-Windows.ps1` | Windows x64 build script |

## Building and testing

See [BUILD.md](BUILD.md) for toolchain requirements, source builds and local tests. The official store item ID is `agbejnpaacdhklaocalpballabcibmgm`. A locally loaded unpacked extension may have a different ID; the installer must use the exact ID of the intended extension. Do not install a development build over an active production companion.

## Privacy and security

The proxy is local and starts after consent. It does not decrypt HTTPS or send browsing logs or analytics to Unwritten Byte. Eligible HTTP data and encrypted HTTPS traffic pass through memory as necessary to relay and pace traffic. The local proxy remains connected in unlimited mode; use **Disconnect proxy** to stop it. The optional VPN mode relaxes specific destination checks and should only be enabled for a trusted VPN or DNS filter. See [SECURITY.md](SECURITY.md) for reporting vulnerabilities.


## Support and maintenance

Speed Limiter is an independent, free, open-source project. Support, bug fixes and future updates are provided on a voluntary, best-effort basis.

There is no guarantee of continued development, compatibility or long-term maintenance.


## License

GNU General Public License, version 3 only (GPL-3.0-only). See [LICENSE](LICENSE). The license applies to the software; it does not grant permission to impersonate Unwritten Byte or imply endorsement by the publisher.

Google Chrome is a trademark of Google LLC. This is an independent project and is not affiliated with Google.
