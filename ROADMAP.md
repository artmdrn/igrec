# igrec Roadmap

This roadmap is ordered so each step leaves the service more real without turning the codebase into a half-built maze. Keep tasks small, shipable, and easy to verify on Oracle.

## Phase 0: Stabilize The Deployed Skeleton

- Replace demo-only posting with a real current-user boundary, even before full auth.
- Add structured config validation on boot for production-required variables.
- Add a health endpoint for uptime checks.
- Add basic request logging and error logs that do not leak secrets.
- Add a deployment checklist for Oracle, Cloudflare, Resend, and GitHub.

Done when:

- `igrec.service` starts only with valid production config.
- `/healthz` returns `200`.
- A fresh deploy can be verified with one documented command sequence.

## Phase 1: Invite-Only Accounts And Magic Links

- [x] Add invite creation and redemption.
- [x] Add users with stable usernames, emails, and session cookies.
- [x] Add magic-link login through Resend.
- [x] Make `/write` require login.
- [x] Make inbound email posting map to a real user email.
- [ ] Replace beta admin invite endpoint with a real operator UI.
- [ ] Add CSRF protection for session-backed form posts.

Done when:

- A new account can be created only with an invite.
- A user can log in without a password.
- Posts are attributed to the logged-in user, not `demo`.

## Phase 2: The One-Word Product Loop

- Harden one-word validation across web, email, and future federation entry points.
- Add duplicate-word-per-user support as intentional post moments.
- Add optional image upload with a conservative size/type policy.
- Build the daily email job: one word from someone followed, plus the plain-text prompt.
- Add email preference settings.

Done when:

- The core loop works: receive prompt, reply with one word, see it posted.
- Users can opt in/out of daily emails.
- Images can be attached safely.

## Phase 3: ActivityPub Basics

- Add actor key generation and HTTP signatures.
- Implement inbox handling for Follow, Undo Follow, Accept, Reject, and Move.
- Store followers and follower inboxes.
- Deliver Create/Note activities to follower inboxes.
- Improve WebFinger, actor, outbox, and object URLs for compatibility.

Deferred:

- Likes, replies, boosts, quote-posts, and public follower counts.

Done when:

- A Mastodon account can follow an igrec user.
- New words appear in that Mastodon home timeline.
- Account Move can redirect followers to another ActivityPub actor.

## Phase 4: IndieAuth And Mastodon OAuth

- Add IndieAuth login for users who own a domain.
- Add Mastodon OAuth login for existing fediverse users.
- Link multiple auth identities to one igrec account.
- Add rel=me verification links on profiles.

Done when:

- Existing fediverse users can sign in without email.
- Domain owners can sign in with IndieAuth.
- Profiles expose rel=me links cleanly.

## Phase 5: Portability And Settings

- Add one-click JSON export.
- Add ActivityPub-flavored export.
- Add account migration UI.
- Add delete-account flow with confirmation.
- Add settings for fediverse handle, rel=me links, email preferences, export, migration, and deletion.

Done when:

- Users can leave with their data.
- Migration and deletion do not require operator intervention.

## Phase 6: PWA And Notifications

- Add service worker and install metadata.
- Add VAPID key validation.
- Add push subscription storage.
- Send Web Push notifications for daily prompt and relevant account events.
- Make notification taps open `/write` with the input focused.

Done when:

- Installed PWA opens straight to the writing flow from a notification.
- The public feed remains browsable without JavaScript.

## Phase 7: Production Hardening

- Add HTTPS origin certificate on Oracle.
- Rotate all keys that appeared in chat.
- Add backups for SQLite.
- Add restore drill documentation.
- Add rate limits for posting, inbound email, auth, and ActivityPub inbox.
- Add basic moderation/operator controls for invite revocation and account suspension.
- Extend CI with integration tests and deployment rollback checks.

Done when:

- A server loss can be recovered from backup.
- Abuse controls exist before wider invites.
- CI runs tests on every push.

## Suggested Next Task

Implement Phase 1 magic-link auth and invite-only registration. This removes the `demo` user placeholder and makes the deployed app usable by real people.

## v2 Ideas

These are deliberately parked until the beta loop is stable. They should not be picked before the Phase 0-7 work unless explicitly requested.

### Public Read API

- Add a read-only public REST API.
- `GET /api/@username/words` returns a user's full word archive as JSON.
- Use the same internal representation to power email and PWA features.

Done when:

- Public archives are available as stable JSON without authentication.
- API output includes enough metadata for clients to render dates according to user preference.

### On This Day

- After one year of activity, daily email gains a second line:
  `On this day last year, you said: [word].`
- Stay completely silent until there is at least one eligible prior-year word.

Done when:

- Daily email includes the line only when a same-month/day word exists from a prior year.
- No placeholder copy appears before the first anniversary.

### Private Streaks

- Add a private streak counter.
- Show streaks only in `/settings` and `/write`.
- Never expose streaks publicly or through public profile pages.
- Email subject shifts from `>` to `>>` if the user has not posted today.
- Keep the tone informational, not guilt-driven.

Done when:

- Users can see their own streak privately.
- Public pages and public APIs do not reveal streaks.
- Daily email subject reflects whether today's word has happened.

### Last Word

- After one year of inactivity, not deletion, an account goes quiet.
- The final word remains permanently in archives.
- The final word is attributed as `·` with no profile link.
- Example display: `· ember`

Done when:

- Inactive accounts stop behaving as active profiles after one year.
- Their final word remains visible and stable.
- Public rendering uses the anonymous dot attribution without linking to the profile.
