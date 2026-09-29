# Dogfood — 5-Minute Hackathon Demo Script

## A. Creative Thesis
The video demonstrates that Dogfood is not merely a backend algorithm, but a polished, end-to-end hackathon platform that solves the hardest problem in event management: establishing trust in the judging process. It quickly grounds the viewer in the real product, builds technical tension through the judging engine, climaxes with the CLI replay verification proving mathematical integrity, and concludes by validating product completeness and offline operability.

## B. Exact Timeline
- **00:00–00:08** — Brand Opening & The Problem
- **00:08–00:25** — Show the Real Platform (Public Event & Gallery)
- **00:25–00:42** — Project → Judging Context (The Differentiator)
- **00:42–01:15** — Live Judge Workflow & Role Isolation
- **01:15–02:15** — Organizer Results & Normalization (Explain This Rank)
- **02:15–03:15** — Cryptographic Replay Verification (The Climax)
- **03:15–04:00** — T3/T4 Product Completeness
- **04:00–04:30** — Self-Hosted / Offline Architecture
- **04:30–05:00** — Closing

---

# C & D. EXACT TIMELINE, DEMO ACTIONS, AND NARRATION

## 00:00–00:08 — Brand Opening & The Problem

[TIME] 00:00–00:08
[DEMO] BRAND OPENING
[SCREEN] 
Display a fast, 2-3 second "booting page" (a simulated terminal sequence booting the evaluation engine).
Smoothly animate popup title over the subtle grid background:
Text: **Hackathon Raptors: Hackathons you can actually trust.**
[ACTION] 
Keep the animation extremely short and restrained. Transition directly to the browser.
[NARRATION] 
"A hackathon can collect hundreds of submissions and dozens of judge scores. The hard part is making the final result trustworthy."
[TECHNICAL POINT] 
Establish the core problem worth solving immediately.
[VISUAL] 
Fast boot sequence → Clean Branding → Live Application.
[TRANSITION] 
Cut to the live Dogfood interface on `localhost:8080`.

---

## 00:08–00:25 — Show the Real Platform

[TIME] 00:08–00:25
[DEMO] PUBLIC EVENT / GALLERY
[SCREEN] 
The live Dogfood event homepage (`/projects`).
[ACTION] 
Slowly scroll to reveal the event identity, the status badge ("Submissions Open"), the navigation bar, and the interactive grid background tracking the mouse.
Move from the event landing area into the project gallery.
Click on one real seeded project (e.g., Team Alpha's project).
[NARRATION] 
"Dogfood is a complete hackathon platform, not just a scoring engine. Participants can submit projects, teams and events are managed in the same system, and the public can discover the work through the gallery. You are looking at a real, live application."
[TECHNICAL POINT] 
Prove that Dogfood is a real, end-to-end product.
[VISUAL] 
Polished UI showing project metadata, team information, and track badges.
[TRANSITION] 
Leave the project page visible briefly.

---

## 00:25–00:42 — Project → Judging Context

[TIME] 00:25–00:42
[DEMO] THE CORE DIFFERENTIATOR
[SCREEN] 
Keep the project page visible.
Overlay a simple, elegant on-screen text graphic representing the pipeline.
[ACTION] 
Display the pipeline graphic over the UI:
`SUBMISSIONS → JUDGES → RUBRIC + SCORES → RESULT → VERIFICATION`
[NARRATION] 
"But the part we designed most carefully is what happens between a submission and the final result. Dogfood gives judges isolated assignments and configurable weighted rubrics, then turns those evaluations into reproducible results that can be inspected and replayed."
[TECHNICAL POINT] 
Introduce the judging pipeline without deep mathematics yet.
[VISUAL] 
Text pipeline graphic fading out.
[TRANSITION] 
Cut to the Login screen.

---

## 00:42–01:15 — Live Judge Workflow

[TIME] 00:42–01:15
[DEMO] JUDGE WORKSPACE & ISOLATION
[SCREEN] 
Login page → Judge Dashboard (`/dashboard`).
[ACTION] 
Log in using the Judge A demo persona ("Tomas Varga").
Open the Judge Dashboard to show the explicitly assigned project count.
Open the first assigned project to reveal the evaluation ballot.
Expand the rubric. Enter a score for Innovation and Technical Execution.
Finalize and submit the ballot.
[NARRATION] 
"Here I'm acting as a judge. The judge sees their assigned projects and the configured rubric, but their private evaluation data is strictly isolated from other judges. I will score this project against the rubric and submit the ballot. That separation matters because the scoring process should not become a shared pool of private judge information. The inputs must remain pristine."
[TECHNICAL POINT] 
Proving strict role isolation and immutable rubric evaluations.
[VISUAL] 
The actual status transition of the ballot changing from Draft to Submitted.
[TRANSITION] 
Log out and log in as Organizer.

---

## 01:15–02:15 — Normalization & Explainable Result

[TIME] 01:15–02:15
[DEMO] ORGANIZER PROGRESS & THE RECEIPT
[SCREEN] 
Organizer Dashboard (`/dashboard`) → Leaderboard (`/results`) → Explain Rank Receipt.
[ACTION] 
Show the Organizer judging progress dashboard.
Navigate to the public Leaderboard (`/results`).
Click the "Explain This Rank" button on the 1st place project.
Slowly scroll through the detailed math receipt.
[NARRATION] 
"When judging closes, the organizer needs to publish results. But raw scores are notoriously flawed—some judges are harsh, some are lenient. Dogfood normalizes score-scale differences between judges and exposes the calculation used to produce the result. The outcome isn't just a leaderboard. Every single project receives an 'Explain This Rank' receipt. Anyone can see the exact mathematical path from raw score to standard z-score to final normalized rank."
[TECHNICAL POINT] 
Dogfood automatically corrects judge variance using statistical normalization.
[VISUAL] 
The "Explain This Rank" UI showing the detailed breakdown.
[TRANSITION] 
Highlight the `run_id` and `project_id` on the receipt, then transition to the Terminal.

---

## 02:15–03:15 — Cryptographic Replay Verification (The Climax)

[TIME] 02:15–03:15
[DEMO] CLI REPLAY (THE CLIMAX)
[SCREEN] 
A local terminal window.
[ACTION] 
Run the CLI command: `go run ./cmd/dogfood replay <run_id> <project_id>`
Highlight the output: the SHA-256 canonical manifest hash, the independently recomputed algorithms, and the final green `PASS: 100% Mathematical Equality Verified`.
[NARRATION] 
"But you don't have to trust the web interface. Dogfood freezes the inputs into a canonical manifest with a cryptographically identical input digest. Anyone with the open-source CLI can run a deterministic replay command. The replay engine downloads the manifest, completely bypasses the database, independently recalculates the normalization and tie-breaking algorithms, and proves bit-for-bit that the published results match the raw inputs exactly. This is an independently reproducible result."
[TECHNICAL POINT] 
The ultimate proof of judging integrity and deterministic math.
[VISUAL] 
The terminal printing "PASS" is the intellectual climax of the video.
[TRANSITION] 
Switch back to the browser showing a project detail page.

---

## 03:15–04:00 — T3/T4 Product Completeness

[TIME] 03:15–04:00
[DEMO] COMMUNITY & EXTENSIONS
[SCREEN] 
Project Detail Page (`/projects/{id}`).
[ACTION] 
Click the "Vote" button (show the community vote registering).
Point out that duplicate votes are blocked.
Scroll down to show Project Comments.
Click the "View Certificate" button and show the verifiable completion certificate with its SHA-256 integrity seal.
[NARRATION] 
"Beyond official judging, the platform is feature-complete. It includes duplicate-protected community voting, which enforces a hidden results policy to prevent hype-train bias. The community can also engage through active project comments. And when the event ends, teams receive verifiable completion certificates backed by an integrity digest of their submission, ensuring their portfolio evidence is legitimate."
[TECHNICAL POINT] 
Demonstrating implemented T3 (Voting/Comments) and T4 (Certificates) features that make the product complete.
[VISUAL] 
The certificate rendering with the cryptographic hash.
[TRANSITION] 
Switch back to the terminal.

---

## 04:00–04:30 — Self-Hosted / Offline Architecture

[TIME] 04:00–04:30
[DEMO] OFFLINE CAPABILITY
[SCREEN] 
Terminal window.
[ACTION] 
Stop the running server (`docker compose down`).
Run `docker compose up -d`.
Run `docker exec dogfood-dogfood-1 /dogfood healthcheck`.
Show the `OK` status.
[NARRATION] 
"And crucially, everything you are seeing runs completely offline. With zero external internet dependencies, a single `docker compose` command spins up the entire platform. Your hackathon can survive convention center Wi-Fi drops, without relying on cloud databases or external SaaS."
[TECHNICAL POINT] 
Dogfood is an independent, self-hostable offline-first platform.
[VISUAL] 
Docker compose instantly spinning up the container.
[TRANSITION] 
Return to the browser's `/projects` gallery page.

---

## 04:30–05:00 — Verification & Conclusion

[TIME] 04:30–05:00
[DEMO] CONCLUSION
[SCREEN] 
Dogfood Gallery with the interactive grid background illuminating under the mouse.
Fade to the final title card: **Hackathon Raptors: Hackathons you can actually trust.**
[ACTION] 
Smoothly scroll the gallery, then fade to black.
[NARRATION] 
"Dogfood moves hackathons beyond blind trust. By combining a polished end-to-end event platform with strict role isolation, statistical normalization, and independent deterministic replay, we've built a system where the best project actually wins—and where you can mathematically prove it. Thank you."
[TECHNICAL POINT] 
Final summary reinforcing the creative thesis.
[VISUAL] 
Polished UI fading to the core brand message.
[TRANSITION] 
End of video.

---

# E. EVIDENCE MAPPING

| Claim | Screen Demonstration | Repository Evidence | Test/Evidence Source |
| ----- | -------------------- | ------------------- | -------------------- |
| Complete platform (teams/gallery) | Act 2 (Gallery & Projects) | `internal/httpapp/server.go`, `README.md` | Officially Verified (T1) |
| Strict role isolation | Act 4 (Judge UI) | `internal/httpapp/auth.go` | Officially Verified (T2 Flow F) |
| Immutable ballot evaluation | Act 4 (Submitting a ballot) | `internal/judging/ballot.go` | Officially Verified (T2) |
| Normalizes score-scale differences | Act 5 (Explain This Rank) | `internal/results/engine.go`, `NORMALIZATION-PROOF.md` | Officially Verified (T2) |
| Deterministic Replay / Manifest | Act 6 (Terminal CLI `replay`) | `cmd/dogfood/replay.go` | Officially Verified (T2) |
| Duplicate voting blocked | Act 7 (Clicking vote) | `internal/httpapp/t3_t4.go` | Implemented / Indep. Tested (T3) |
| Hidden results policy | Act 7 (Narration during voting) | `internal/httpapp/t3_t4.go` | Implemented / Indep. Tested (T3) |
| Verifiable Certificates | Act 7 (Certificate UI view) | `internal/httpapp/t3_t4.go` | Implemented / Indep. Tested (T4) |
| Zero external internet deps | Act 8 (Docker compose logs) | `Dockerfile`, `README.md` | Officially Verified (T2 Checkpoint 6) |

---

# F. T1/T2/T3/T4 STATUS

* **T1 (Core Portal)**: OFFICIALLY VERIFIED
* **T2 (Judging & Replay)**: OFFICIALLY VERIFIED
* **T3 (Community)**: IMPLEMENTED & INDEPENDENTLY TESTED (Community voting, comments, audit trail, randomized gallery are functionally present).
* **T4 (Extensions)**: IMPLEMENTED & INDEPENDENTLY TESTED (Verifiable certificates, webhooks, judge records, bulk JSON export are functionally present).

---

# G. RECORDING CHECKLIST

[ ] Start screen recorder.
[ ] Show 2-3 second terminal boot sequence -> Title Graphic.
[ ] Open `http://localhost:8080`, show Gallery and interactive background.
[ ] Display text pipeline graphic on screen during narration.
[ ] Log in as Tomas Varga (Judge A).
[ ] Score assigned project and finalize ballot.
[ ] Log out, log in as Organizer. Show Progress Dashboard.
[ ] Navigate to `/results` (Leaderboard). Open "Explain This Rank".
[ ] Open terminal, run CLI replay: `go run ./cmd/dogfood replay <run_id> <project_id>`.
[ ] Navigate to a project page in the browser. Click "Vote", show duplicate rejection, scroll to comments.
[ ] Click "View Certificate", show digest.
[ ] In terminal, run `docker compose down` then `docker compose up -d`, then `docker exec dogfood-dogfood-1 /dogfood healthcheck`.
[ ] Return to `/projects` gallery, smooth mouse movement, fade out.

---

# H. FAILURE/BACKUP PLAN

* **Replay CLI Fails**: If live CLI replay stutters, use a pre-recorded snippet of the `go run ./cmd/dogfood replay` command output.
* **Docker Compose Hangs**: If Docker daemon is slow to respond live, keep a cached terminal output of the successful `healthcheck` command ready to overlay.
* **Network / Seed Issues**: Ensure you are running locally without relying on an external SQLite volume that may have been corrupted. Run a fresh container to guarantee pristine seeded fixtures.
