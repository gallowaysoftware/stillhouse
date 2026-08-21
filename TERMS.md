# Stillhouse — terms of use

**Status: a starting point, not a reviewed legal document.** These terms
were drafted to close the gap identified in `PLAN.md` as `H1`: the AGPL
disclaims the *software* (sections 15 and 16) and says nothing about a
*hosting relationship*. If you host Stillhouse for anyone other than
yourself, have a lawyer read this before you rely on it. Nobody who wrote
it is one.

Last updated: 2026-08-21.

---

## 1. What Stillhouse is

Stillhouse is record-keeping software for a CRA-licensed spirits producer.
It records production, tracks alcohol through a ledger, and **computes
figures that are intended to correspond to the lines on CRA Form B266 and
related returns**.

## 2. What Stillhouse is not

Stillhouse is not an accountant, an excise consultant, or a filing agent.

**Stillhouse never files anything with the Canada Revenue Agency.** There
is no integration, no submission, and no queue. Marking a period submitted
inside Stillhouse freezes a snapshot for your own audit trail and does
nothing else. Every return is filed by a person, by hand, through CRA My
Business Account or another channel CRA accepts.

The figures Stillhouse produces are **your** figures, computed from data
**you** entered. You remain the licensee. Section 206 of the Excise Act,
2001 puts the record-keeping obligation on you, and nothing in this
software moves it.

## 3. Verify before you file

You are asked to confirm, each time you mark a period submitted, that you
have checked the figures against your own records. That confirmation is
recorded with your name and the date, and it is not a formality: it is the
moment the numbers stop being a computation and start being a filing.

Where Stillhouse cannot compute something honestly it refuses rather than
guessing — an excise rate outside the range it can cite, a strength it
cannot correct without the alcoholometric tables, a loss nobody has ruled
on. A refusal is the software working. Do not work around it by entering a
figure you have not verified.

## 4. No warranty

The software is provided **as is**, without warranty of any kind, express
or implied, including but not limited to the warranties of
merchantability, fitness for a particular purpose, and non-infringement.
This restates sections 15 and 16 of the GNU Affero General Public License,
which govern the software itself and are reproduced in `LICENSE`.

To the maximum extent permitted by applicable law, the authors and any
party hosting Stillhouse on your behalf are not liable for any claim,
damages, or other liability arising from the use of the software —
including, without limitation, penalties, interest, or assessments arising
from a return you filed.

Some jurisdictions do not allow the exclusion of certain warranties or
liabilities. Where that is so, the exclusions above apply only to the
extent permitted.

## 5. If someone else hosts this for you

An operator hosting Stillhouse for you should tell you, in writing:

- where your data physically lives;
- how often it is backed up, and when a restore was last tested;
- how long backups are kept, and how they are protected;
- what happens to your data if the arrangement ends;
- who at their end can read it.

Stillhouse's own answers to those questions, for an install you run
yourself, are in [`docs/operations.md`](docs/operations.md). An operator
hosting for others should publish theirs.

Under section 206(1) of the Excise Act, 2001 you must keep records
sufficient to determine your compliance. Six years is the working
retention window. **A hosted arrangement does not discharge that
obligation** — satisfy yourself that you can produce your own records
independently of whoever hosts them. Stillhouse's tenant export exists for
exactly that: Settings → Export.

## 6. Your data

Your data is yours. Stillhouse does not transmit it anywhere. There is no
telemetry, no analytics, and no outbound connection made on your behalf
except to services you configure yourself.

Personal information in Stillhouse is limited to what running a distillery
needs: the names and email addresses of the people with accounts, and the
audit trail of what each of them did. Under PIPEDA, and under Quebec's
Law 25 for a Quebec licensee, the person hosting the install is
accountable for that information.

## 7. Changes

These terms may change as Stillhouse does. The version in the repository
at the commit you are running is the one that applies. Material changes to
a hosted service should be told to the people using it before they take
effect, not after.
