# Building from source

## Windows x64

Use a currently supported Go release (the code requires Go 1.21 or later), LLVM clang with Windows COFF target support, and PowerShell. No Go third-party modules are required. Record exact toolchain versions and hashes for release builds. The original local development build used Go 1.23.2; use a supported version for distribution and retest it.

From the repository root, run `./Build-Windows.ps1`. It builds `installer/companion.exe`, generates the Windows resource object from the assembly and XML manifest, then embeds the companion in `dist/SpeedLimiter-Setup.exe`. Generated files are excluded from version control. This script does not sign either executable.

For a locally loaded extension, open `chrome://extensions`, enable Developer mode, select Load unpacked and choose `extension/`. Copy the actual ID shown there. Build with `./Build-Windows.ps1 -ExtensionId <actual-id>` (replace the placeholder with the ID). Close all Chrome processes before running the resulting installer. The installer only registers the chosen ID. For an official release, omit the ID override.

When signing is available, sign `installer/companion.exe` first, then run `./Build-Windows.ps1 -PackageOnly` (with the same ID if overriding it). Sign and timestamp the resulting setup executable last, then verify both signatures. Rebuilding after signing invalidates the signature of the rebuilt artifact. An independent signing service may need a different, audited build workflow.

## Automated tests

Install Node.js and Python 3 for tests only. On a POSIX shell, from the repository root:

```sh
(cd host && GO111MODULE=off go test -v)
(cd host && GO111MODULE=off go build -o /tmp/speed-limiter-host .)
python3 tests/test_host.py /tmp/speed-limiter-host
node tests/test_extension.js
node tests/test_popup.js
```

The Node tests mock Chrome; Linux host tests do not exercise Windows process ownership checks or Windows registry integration. A Windows x64 cross-build tests compilation only.

## Release validation

Before distributing: test the exact packaged extension and companion on clean Windows, confirm the official extension ID, cap accuracy and shared downloads, live toggling, regular browsing, optional VPN behavior, browser restart, unexpected companion exit, extension removal and uninstall cleanup. Confirm the download location, checksums, signing status, privacy policy and support details. Do not disable antivirus or Windows security protections to run tests.
