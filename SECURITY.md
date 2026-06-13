# Security Policy

`passage` is security-adjacent software: it shells out to `pass` and `gpg`,
copies secrets to the clipboard, and displays secrets only after explicit user
action.

## Reporting

Please report vulnerabilities privately to:

```text
info@offcourtcreations.com
```

Include reproduction steps, affected versions or commits, and whether the issue
can expose decrypted secret material.

## Supported Versions

Only the latest released version is supported for security fixes.

## Design Notes

- GNU Pass remains the source of truth.
- TOTP secrets are never passed as command arguments.
- `show` and `list` expose metadata only.
- Secret previews are intentionally absent from the picker.
- Clipboard providers receive secret bytes over stdin.
