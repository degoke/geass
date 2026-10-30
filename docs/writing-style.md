# Writing style for user documentation

This guide applies to the public documentation in `website/src/content/docs/` and to the repository `README.md`. It
combines two standards:

- The [Google developer documentation style guide](https://developers.google.com/style).
- The writing rules of [ASD-STE100 Simplified Technical English](https://www.asd-ste100.org/) (STE).

When the two standards disagree, use the stricter rule. Geass uses the STE writing rules. It does not use the STE
dictionary, because the docs need technical names and product terms that the dictionary does not have.

## Sentences

- Write one instruction or one idea in each sentence.
- Keep a procedural sentence (an instruction) to 20 words or fewer. Keep a descriptive sentence to 25 words or fewer.
  To shorten a sentence, split it, or change it into a list.
- Use the active voice. Name the thing that does the action: you, Geass, the operator, or the installer. Use the
  passive voice only when the actor is not known or not important.
- Use the present tense. Do not use "will" to say what the product does. Write "Geass creates the Secret", not "Geass
  will create the Secret".
- Speak to the reader as "you". Do not use "we", "the user", or "one".
- Use the imperative for instructions: "Open **Settings**."
- Put the condition or the goal before the instruction: "To delete the project, open **Settings**." "If the pod does
  not start, read its events."
- Do not use contractions. Write "do not", "is not", and "cannot".
- Do not drop articles. Write "Open the project and select the database", not "Open project, select database".
- Do not use an -ing word as a noun or after a preposition. Write "Before you deploy", not "Before deploying".
- Make each "this", "that", and "it" clear. If the noun is not clear, repeat the noun: "This setting", "That Secret".

## Paragraphs and lists

- Write one topic in each paragraph, with six sentences or fewer.
- Use a numbered list for steps that the reader does in sequence. Write one action in each step, except for actions
  that the reader does at the same time.
- Use a bulleted list for items that have no sequence.
- Introduce a list with a full sentence that ends with a colon.
- Make the items in a list parallel: all sentences, or all fragments. End each sentence with a period.

## Words

Use one word for one meaning, and one meaning for one word.

| Use | Do not use |
| --- | --- |
| **click** (a button or a link) | choose, hit, press (for a button) |
| **select** (an option in a list, a menu, a tab, or a check box) | choose, pick |
| **turn on**, **turn off** (a switch) | enable, disable, toggle (in instructions) |
| **enter** (a value in a field) | type in, fill in, put |
| **can** (ability) | is able to, allows you to, lets you |
| **might** (possibility) | may (for possibility) |
| **after**, **when** | once (except to mean "one time") |
| **because** | since (to mean "because"), as (to mean "because") |
| **but**, **although** | while (to mean "although") |
| **immediately** | at once, right away |
| **make sure** | ensure, be sure to |
| **use** | utilize, leverage |
| **for example** | e.g., such as (at the start of an example list is fine) |
| **that is** | i.e. |
| **and so on** | etc. |
| **through**, **with**, **by** | via |
| **to** | in order to |

Do not use "simply", "just", "easy", "easily", "obviously", "please", or "note that". Prefer one verb to a phrasal verb:
"remove", not "take out"; "find", not "find out". The UI terms "set up", "sign in", and "sign out" are allowed.

Use the American spelling. Use the serial comma: "projects, environments, and services".

## Headings

- Use sentence case.
- Start a task heading with a bare verb: "Create a database", not "Creating a database".
- Use a noun phrase for a concept heading: "Backup storage".
- Heading text creates the link anchor. If you change a heading, update every link to its anchor.

## Formatting

- Use **bold** for the names of UI elements. Copy the label exactly as the dashboard shows it.
- Use `code` for commands, flags, file names, paths, field names, resource kinds, and values.
- Copy messages that the product shows exactly, in `code`, even when they do not follow this guide.
- Do not change code blocks to follow this guide. Comments in a code block follow the guide where possible.
- Write link text that says what the link opens. Do not use "here" or "this page" as link text.
- Do not put information that the reader needs in parentheses. Write it in its own sentence.
- Do not use semicolons. Write two sentences.
- Spell out an abbreviation the first time that a page uses it, except for common terms such as API, CPU, DNS, HTTPS,
  IP, SSH, and URL.

## Notes and warnings

Use the Starlight admonitions:

- `:::note` for information that helps the reader.
- `:::tip` for a better way to do a task.
- `:::caution` for a risk of a failure, an outage, or a cost.
- `:::danger` for a risk of data loss or a security risk.

Start a caution or a danger with the instruction. Then give the reason: "Do not delete the namespace. Geass deletes
the database volumes with it."

## Page descriptions

The `description` in the front matter is the search result snippet. Keep it from 50 to 160 characters, and make it
different from every other page. `pnpm seo` checks both rules.

## Check your changes

```bash
cd website
pnpm build
pnpm links      # every internal link and heading anchor
pnpm seo        # titles, descriptions, and sharing tags
```
