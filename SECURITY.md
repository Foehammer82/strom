# Security Policy

## Reporting A Vulnerability

Do not open public GitHub issues for suspected security vulnerabilities.

Preferred reporting path: use GitHub's private vulnerability reporting flow for
this repository.

1. Open the repository on GitHub.
2. Go to the Security tab.
3. Choose Report a vulnerability.
4. Submit the details privately.

If the Report a vulnerability option is not available, the repository owner
needs to enable private vulnerability reporting in the repository's security
settings.

Please include:

- A clear description of the issue and the affected component
- Steps to reproduce or a proof of concept
- The affected version, tag, or commit if known
- Any relevant logs, screenshots, or configuration details
- Your assessment of impact or exploitability, if you have one

We will acknowledge new reports as soon as practical and aim to provide an
initial triage update within 3 business days.

## Supported Versions

Security fixes are handled on a best-effort basis and are normally limited to:

- The most recent stable tagged release
- The most recent prerelease tag when that prerelease is the active release candidate
- The current `main` branch before the next tagged release is cut

Older tags, superseded release candidates, and untagged historical snapshots
should be assumed unsupported unless we explicitly state otherwise for a
specific incident.
