#!/bin/bash
# Local test run: real Frappe + WhatsApp from .env (git-ignored). Pass ADMIN_MOBILE=<your number> in the environment.
set -a; source "$(dirname "$0")/.env"; set +a
# Real WorkCodes are sent on WhatsApp. Set DEV_OTP=1 in the environment only if you want the code shown on screen.
exec ./rapid-entry
