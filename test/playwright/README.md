# Dashboard browser smoke tests

Run the dashboard against a running Geass manager, then execute:

```sh
npm install
npx playwright install chromium
npm test
```

Override the dashboard address with `GEASS_DASHBOARD_URL`.
