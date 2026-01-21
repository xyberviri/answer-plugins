# Answer Privacy Plugin

This plugin filters Answer API responses so users only see answers they created. It inspects the answer detail and answer list API responses and removes any answers whose `user_info.id` does not match the current authenticated user.

## Behavior

- `GET /answer/api/v1/answer/info`
  - Returns the answer only when the requesting user is the author.
  - Otherwise responds with `403` and an empty `data` payload.
- `GET /answer/api/v1/answer/page`
  - Filters the response list to contain only answers created by the current user.

## Configuration

Enable or disable the plugin from the Answer plugin settings. The default is enabled.

## Important Notes

This plugin registers middleware on authenticated routes. If your Answer deployment exposes answer routes to unauthenticated users, you should require login for answer APIs or move these routes into authenticated groups in your Answer build so the middleware can run.
