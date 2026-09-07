# kavel-go

Generate **and edit** images from Go with no API key, no account and no card.

```bash
go get github.com/hanshs474/kavel-go
```

```go
img, err := kavel.Generate(context.Background(),
    "matte black ceramic mug on pale oak, soft window light from the left, shallow depth of field",
    kavel.Options{AspectRatio: "16:9"})
// img.URL         https://cdn.kavel.ai/uploads/kie/image/....webp
// img.Watermarked true on the free tier
```

```go
img, err := kavel.Edit(context.Background(), sourceURL,
    "place the mug on a white marble surface with a sprig of rosemary beside it",
    kavel.Options{})
```

Or without writing any code:

```bash
go run github.com/hanshs474/kavel-go/example@latest "an isometric coffee shop, pastel palette"
go run github.com/hanshs474/kavel-go/example@latest -edit https://example.com/portrait.jpg "give him a buzz cut"
```

Every other image client wants a key from OpenAI, fal or Replicate before it runs once. This one
talks to the anonymous tier of [Kavel](https://www.kavel.ai/?utm_source=pkggodev&utm_medium=package) — a client id
the package invents rather than an account you register — so it works on a machine with nothing
configured.

## What you get

- **An edit lane, not just generation.** Most no-key clients can only make a new picture. `Edit`
  takes a photo you already have and a sentence describing the change.
- **Zero dependencies.** Standard library only, so there is nothing to pin and nothing that can
  drift out from under you.
- **A url, not bytes.** The returned CDN link is permanent and cacheable, and `Watermarked` says
  whether a mark was actually drawn rather than leaving you to assume.
- **Errors you can branch on.** `ErrQuota`, `ErrRejected` and `ErrSignIn` are sentinel errors;
  `errors.Is` tells you whether to wait, to reword, or to sign in.
- **It keeps polling through a dropped connection.** A free run sits in a queue and is only sent to
  the model at the end of it, so abandoning the wait on one failed poll would throw away a job that
  was about to run.

## Limits, read out of the running service

Measured 2026-09-07 by calling it, not estimated:

- The anonymous grant is **15 credits**. One generated image costs **5**, so a fresh client id buys
  **three**; the package mints one per call, so a loop is not capped at three.
- **One edit costs 15** — the whole grant, so a client id buys exactly one edit.
- A ceiling of **30 credits per IP per day** sits on top: about **six images**, or two edits, from
  one machine. That surfaces as `ErrQuota`.
- Free output is **1K and watermarked**.
- Free runs wait **25–80 seconds** in a queue before the model is called at all. That is why the
  default deadline is six minutes and why polling is not optional.
- 🔴 **Video cannot run anonymously.** The cheapest clip costs more than the grant can pay at any
  setting, so this package does not pretend to offer one — it returns `ErrSignIn`. The
  [video generator](https://www.kavel.ai/video?utm_source=pkggodev&utm_medium=package) has a free browser lane instead.

Signing in removes the watermark, lifts the ceiling and opens the rest of the shelf:
[Nano Banana 2](https://www.kavel.ai/image/nano-banana-2?utm_source=pkggodev&utm_medium=package) for generating or editing from up to 14
reference photos, [GPT Image 2](https://www.kavel.ai/image/gpt-image-2?utm_source=pkggodev&utm_medium=package) for 1K–4K output,
[Qwen Image 3](https://www.kavel.ai/image/qwen-image-3?utm_source=pkggodev&utm_medium=package) when the picture has to contain writing
that is spelled correctly, and [Seedream 5.0 Pro](https://www.kavel.ai/image/seedream-5-pro?utm_source=pkggodev&utm_medium=package) for
photoreal people and motion. [Pricing](https://www.kavel.ai/pricing?utm_source=pkggodev&utm_medium=package) lists what each tier costs.

## Writing prompts that work

Name the light, the material and the composition. "A product photo of a mug" gives the model
nothing; "matte black ceramic mug on pale oak, soft window light from the left, shallow depth of
field" gives it a picture. Finished output is in the
[showcase](https://www.kavel.ai/showcases?utm_source=pkggodev&utm_medium=package), and the
[blog](https://www.kavel.ai/blog?utm_source=pkggodev&utm_medium=package) works through longer examples.

For edits, say what changes and leave the rest alone — the model keeps the source framing. The same
lane backs the [hairstyle changer](https://www.kavel.ai/image/ai-hairstyle-changer?utm_source=pkggodev&utm_medium=package) and the
[product photo editor](https://www.kavel.ai/image/ai-product-photo-editor?utm_source=pkggodev&utm_medium=package) on the site, and it is
[Nano Banana 2 Lite](https://www.kavel.ai/image/nano-banana-2-lite?utm_source=pkggodev&utm_medium=package) underneath.

The same engines back the [free AI image generator](https://www.kavel.ai/image?utm_source=pkggodev&utm_medium=package) and the
[ChatGPT image generator](https://www.kavel.ai/image/chatgpt-image-generator?utm_source=pkggodev&utm_medium=package) in a browser.

## Testing

```bash
go test ./...
```

The tests do not hit the network. They cover argument validation, per-call client id uniqueness,
the quota wall (which answers HTTP 200 and can only be recognised by a field), the sign-in refusal,
deadline propagation, and that a dropped poll does not abandon a running job.

MIT.
