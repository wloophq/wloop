# Security policy

## Reporting a vulnerability

Report it privately through GitHub's private vulnerability reporting: on the repository's **Security** tab, choose **Report a vulnerability**. Do not open a public issue for it.

## Supported versions

Only the latest release gets security fixes.

## Threat model

It runs agents as you, with your GitHub and Slack identities, and its checks protect against an agent's mistakes, not against a process running as your user. The [threat model](https://wloophq.github.io/wloop/concepts/security/) says what it protects, what it does not, and which settings limit what reaches an agent from other people.
