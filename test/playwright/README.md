# Dashboard browser smoke tests

Run the dashboard against a running Geass manager, then execute:

```sh
npm install
npx playwright install chromium
GEASS_DASHBOARD_PASSWORD=<password> npm test
```

Override the dashboard address with `GEASS_DASHBOARD_URL`.
Sign in with `GEASS_DASHBOARD_USERNAME` (default `admin`) and `GEASS_DASHBOARD_PASSWORD`.
Create `geass-dashboard-auth` first; Geass does not invent a login password.
