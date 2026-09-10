# Builtin assistant pet asset

This directory holds the shipped desktop pet character:

| File | Role |
| --- | --- |
| `assistant-default.riv` | The runtime asset the renderer loads (artboard `Pet`, state machine `PetSM`, view model `PetVM`). |
| `assistant-default.pack.json` | The pack definition the Go side embeds and the renderer drives the asset with. |

## How the two files relate

`assistant-default.pack.json` is the single source of truth for the
character's data bindings: which view model property each activity slot
(`phase`, `tool`, `walking`, `sleeping`, `intent:<name>`) writes, and
how activity values are translated into the asset's vocabulary. Go
embeds it (`pet.BuiltinAssistantPack`), and the frontend reads the same
file in `frontend/src/pet/assetContract.test.ts`, so the picture and the
driver cannot drift apart.

`assistant-default.riv` is **not** hand-editable. It was produced from an
input-driven base asset by an asset migration generator that rewires the
character to drive through the view model instead of state machine
inputs. Neither the base asset nor the generator lives in this
repository, so regenerating the file is a local, out-of-band step; only
the resulting bytes are committed. The generator asserts that every
object other than the state machine/view model machinery is
byte-identical between the base and the output, so the migration cannot
change how the character looks.

## What a migration must preserve

The contract the renderer relies on is checked on every mount, and
pinned in CI by `frontend/src/pet/assetContract.test.ts` (which loads the
real `.riv` through the official `@rive-app/canvas-advanced` runtime):

- one artboard `Pet` whose state machine is `PetSM` and whose default
  view model is `PetVM`, with exactly the properties `phase` and `tool`
  (string), `walking` and `sleeping` (boolean) and the `wave`, `look`,
  `sulk`, `zoomies` triggers;
- zero state machine inputs: contract v2 drives the character through
  view model values and triggers only, and an input left over from the
  input-driven base would silently keep the old wiring;
- one animation per reachable state, so every value the pack can write
  lands on a pose.

Any change to the asset therefore has to keep that test green; if a
migration legitimately changes the vocabulary, update the test and
`assistant-default.pack.json` in the same commit.
