# Archive UI Demo · Design QA

## Evidence

- Source visual truth: user-supplied Bilibili screenshot showing a formal collection with independent BVID members
- Supporting source: user-supplied Bilibili screenshot showing a multi-P video selection
- Existing demo style reference: earlier archive UI screenshot supplied during design review
- Browser-rendered implementation: `docs/archive-ui-demo-qa.png`
- Side-by-side comparison input: temporary QA artifact (not stored in the repository)
- Viewport: 1280 × 720 baseline; responsive checks at 800 × 900, 560 × 900, and 390 × 844
- State: 正式合集成员选中，合集导航和版本管理同时可见

## Findings

- No actionable P0/P1/P2 finding remains.
- Typography: follows the existing demo's system-font hierarchy; long titles and identifiers truncate inside timeline and navigator rows without overflow.
- Spacing and layout: the approximate 1:3 timeline/watch split is preserved. Relationship navigation forms two readable columns on desktop and one column at the narrow breakpoint.
- Colors and tokens: new collection, part, and version states reuse the existing pink/blue/green token system in both light and dark modes.
- Image quality: no new imagery was required; the existing demo thumbnail/player treatment remains unchanged. The Bilibili screenshots were used as structural, not pixel-clone, references.
- Copy and content: UI now explicitly distinguishes 「正式合集」「视频列表」「视频选集」「版本管理」 and explains the ID boundary for frontend/backend integration.

Focused comparison was required for the relationship area because the reference's key evidence is the right-side `(n/total)` collection list. The implementation reproduces that relationship as a dedicated container navigator while keeping the project's existing layout and visual language.

## Interaction QA

- Collection update event returns to the original subject; switching 8/12 to 9/12 changes BVID and CID.
- P1 to P18 keeps the same BVID and changes CID, duration, title, and active counter.
- Version v4 to v2 changes the archived source while retaining the subject and its timeline position.
- Batch parent selection selects all four versions; a child version can be selected independently.
- Right-click delete opens the centered confirmation dialog; cancel closes it without deletion.
- Theme cycles in order: light, dark, system.
- No horizontal overflow at the tested responsive widths.
- Browser console: no warning or error entries.

## Comparison History

- Pass 1: no P0/P1/P2 visual mismatch was found. The implementation intentionally does not clone the complete Bilibili playback page; it borrows the verified collection/selection hierarchy inside the existing Bilidown demo shell.

## Follow-up Polish

- P3: replace illustrative gradient thumbnails with actual archived covers when the real frontend connects to archive metadata.

final result: passed
