# Preview workflow

When you have finished implementing a requested change and the repository contains a runnable web application:

1. Start the project's normal development server.
2. Ensure it binds to localhost and identify its HTTP port.
3. Choose a short, DNS-safe name describing the task.
4. Run: `preview http <port> --name <task-name> --json`
5. Verify the command succeeds.
6. Give the returned HTTPS URL to the user.
7. Keep the app process and preview running while awaiting review.
8. Do not commit or push merely because the preview succeeded.
9. If the user approves: `preview stop <id>`, stop the development server, run the repository's required checks, then commit and push according to the user's normal Git workflow.
10. If the user requests changes, keep working and reuse or recreate the preview as appropriate.
11. Never expose database ports, admin interfaces, or non-HTTP services through preview.
