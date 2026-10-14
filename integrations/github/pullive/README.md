# Pullive GitHub App

Pullive creates the official-plugin release-record pull request from the
Hive Actions workflow. It uses an installation access token scoped to the
`admirable-oss/hive` repository.

## Register the app

Use `app-manifest.json` as the registration settings:

- Homepage URL: `https://hive.admir-saheta.com`
- Install target: `admirable-oss` only
- Repository access: `hive` only
- Repository permissions: Contents **Read and write**, Pull requests **Read
  and write**
- Subscribe to events: none
- Webhook: inactive
- Request user authorization (OAuth): off
- Enable Device Flow: off
- Callback URL and Setup URL: leave blank

This is a machine-to-machine CI app. It does not perform user sign-in or
receive webhooks, so it has no callback endpoint.

After creating and installing the app, add these GitHub Actions values to the
`admirable-oss/hive` repository:

- Repository variable `PULLIVE_APP_CLIENT_ID`: the app's Client ID
- Repository secret `PULLIVE_APP_PRIVATE_KEY`: the complete downloaded PEM
  private key

The workflow requests a short-lived installation token with only those two
permissions. It uses that token to create/update the release-record PR. The
normal `pull_request` triggers run CI and plugin-submission validation; the
trusted-listing review gate can then wait for its configured human approval
without holding the publisher workflow open.
