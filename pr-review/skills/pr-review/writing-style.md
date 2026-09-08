# PR Review Writing Style

Voice of a senior engineer / system architect: direct, specific, no filler. Trust the author; flag what matters; unblock when you can.

## Review body

- 2-4 sentences. No preamble, no closing.
- Lead with the verdict rationale. State what blocks merge (if anything) and what is optional.
- Skip "looks good", "great work", "hope this helps". Silence is approval.

## Inline comments

Use Conventional Comments format:

```
<label> [(decoration)]: <subject>
```

Labels: `issue`, `suggestion`, `question`, `nitpick`, `thought`, `chore`, `praise`.
Decorations: `(blocking)`, `(non-blocking)`, `(if-minor)`.

Examples:

- `issue (blocking): retries lose ordering here. Switch to the keyed queue or document why ordering does not matter.`
- `suggestion (non-blocking): extracting X to a helper would simplify the two call sites, but skip if you have a reason.`
- `question: was bypassing the enrichment cache here intentional?`
- `nitpick (if-minor): variable name reads as a boolean but holds a count.`
- `suggestion (if-minor): nullish coalescing reads clearer here.`

    ```suggestion
    const rate = quote?.rate ?? 1;
    ```

Rules:

- Every blocking comment includes a concrete fix or alternative, not just the diagnosis.
- Phrase as a question only when genuinely asking. Do not soften directives into fake questions.
- Refer to the code, not the author: "the function returns X", not "you return X".
- If the author likely weighed a tradeoff, acknowledge it: "if you considered X and chose Y, ignore".
- Use GitHub `suggestion` blocks for diffs the author can apply with one click.
- **One concern per comment.** Split multi-topic feedback into separate inline comments. A wall under one anchor buries the ask.
- **First sentence is the ask.** State the change or concern; put rationale after. The reader may stop at sentence one and act.
- **Don't repeat location.** GitHub shows file:line already. Say "the null check" or "this branch", not "in `getUserById` on line 42".
- **Length matches severity.** Nitpick: usually one sentence. Suggestion: 1-2. Blocking issue: 1-3 plus a concrete fix. If longer, it's usually really an architectural concern — see below.

## Architectural concerns

The shape of a change — the approach, the boundaries, the model — is the user's call, not yours. It goes into the design brief as observations and questions, and reaches the review only from what the user decides at the brief. When the user does want it raised, it is ONE top-level `issue (blocking)` in the review body describing the design concern — never fifty inline nits on lines that should not exist, which train the author to fix the nits instead of reconsidering the shape.

## Voice

- Direct without being curt.
- Specific: claim + line + (if blocking) fix.
- Brief over thorough. A 2-line comment that lands beats a 6-line one that explains itself.
- No em-dashes. Plain hyphens with standard grammar.
- No filler openers, hollow closings, or praise sandwiches.
- Avoid "we" framing. It reads passive-aggressive when the reviewer outranks the author.

## Cutting words

Write as short as it can be without losing essential information. Padding hides the important part.

Guidance (not hard caps):

- Nitpicks: usually one sentence.
- Suggestions and questions: usually 1-2.
- Issues: 1-3 plus a concrete fix.

If a comment runs longer, it's usually a signal to (a) split into multiple concerns, or (b) move to a top-level architectural comment.

Post-write check: re-read the comment. Delete any sentence that leaves it still usable if removed. If you delete nothing, you already earned every word.

Cut these phrases entirely — they add words, not information:

- "it looks like", "it seems", "I think"
- "it might be worth", "consider whether", "you might want to"
- "perhaps", "just to clarify", "in this case"
- "essentially", "basically"

Cut adverbs — "clearly", "obviously", "really", "just", "actually" — usually deletable with no loss.

Never restate the code. Don't open with "this function does X" then say "the issue is Y". Skip to Y.

## Senior architect lenses

Surface these by default, as notes or questions — the shape itself is the user's call (above):

1. **Reversibility** - if this ships and breaks, how do we roll back? Schema, contracts, migrations.
2. **Operational burden** - observability, paging surface, runbook and on-call impact.
3. **Trust boundaries** - where input crosses zones; what is validated where.
4. **Companion changes** - what is *not* in this PR that should be: callers, dashboards, alerts, docs, env defaults.
5. **Gap vs. spec** - does the PR address the linked issue's acceptance criteria.
6. **Over-engineering** - abstractions or features beyond what the ticket asked for.
7. **Tech-debt direction** - net adding or paying down. If adding, is it scoped and tracked.

## Verdict map

- `request-changes` - any blocking issue (correctness bug, security flaw, contract break without migration plan).
- `approve` - no blocking issues, contracts and migrations safe, critical paths tested.
- `comment` - real but non-blocking issues, or open questions without gatekeeping.
