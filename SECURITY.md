# Security policy

Please report vulnerabilities privately through GitHub:
[Report a vulnerability](https://github.com/pswies/kodipilot/security/advisories/new)
(the repository's **Security** tab). Do not open a public issue.

You can expect an answer within a week. Fixes go into the latest release; older
releases are not patched.

Note that Kodi's web server uses HTTP Basic authentication without TLS, so the
password crosses the network in clear text. Use it on a trusted network, or
run kodipilot on the Kodi machine over SSH.
