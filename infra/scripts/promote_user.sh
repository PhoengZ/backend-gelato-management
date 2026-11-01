#!/usr/bin/env sh
# Promote an existing account to STAFF or MANAGER in the Auth database.
#
# Public signup only creates CUSTOMER accounts, and Auth has no role-management
# endpoint. Sign up through the website first, then run on the server:
#
#   infra/scripts/promote_user.sh manager@example.com MANAGER
#
# The user must log out and back in: the role is read when Auth issues a token.
set -eu

if [ "$#" -ne 2 ]; then
  echo "usage: $0 <email> <CUSTOMER|STAFF|MANAGER>" >&2
  exit 64
fi

email=$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')
role=$(printf '%s' "$2" | tr '[:lower:]' '[:upper:]')
case "$role" in
  CUSTOMER|STAFF|MANAGER) ;;
  *) echo "role must be CUSTOMER, STAFF or MANAGER" >&2; exit 64 ;;
esac

# psql variables (:'email') are quoted by psql itself, so the address is never spliced into SQL.
updated=$(docker exec -i auth-postgres sh -c \
  'psql -At -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v email="$1" -v role="$2"' \
  sh "$email" "$role" <<'SQL'
\set QUIET on
UPDATE users SET role = :'role', updated_at = NOW() WHERE email = :'email' RETURNING email || ' -> ' || role;
SQL
)

if [ -z "$updated" ]; then
  echo "no account with email $email; sign up on the website first" >&2
  exit 1
fi
echo "$updated (log out and back in to receive the new role)"
