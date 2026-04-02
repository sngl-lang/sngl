---
title: "SNGL"
order: 0
description: "A purpose-built language for reactive, cross-platform UIs"
---

## What is SNGL?

SNGL is a purpose-built language for describing reactive user interfaces that compile to multiple languages and platforms. Write your UI once, and SNGL compiles it to Web, Desktop, Mobile, and TUI targets.

<div class="platform-carousel">
  <div class="carousel-slides">
    <div class="slide active" data-platform="html">
      <img src="assets/snapshots/showcase/app-html.png" alt="SNGL Component Showcase on Web (HTML/JS)">
    </div>
    <div class="slide" data-platform="bubbletea">
      <img src="assets/snapshots/showcase/app-bubbletea.png" alt="SNGL Component Showcase on Terminal (BubbleTea)">
    </div>
    <div class="slide" data-platform="fyne">
      <img src="assets/snapshots/showcase/app-fyne.png" alt="SNGL Component Showcase on Desktop (Fyne)">
    </div>
    <div class="slide" data-platform="android">
      <img src="assets/snapshots/showcase/app-android.png" alt="SNGL Component Showcase on Android">
    </div>
  </div>
  <div class="carousel-tabs">
    <button class="active" onclick="showSlide('html')">Web</button>
    <button onclick="showSlide('bubbletea')">Terminal</button>
    <button onclick="showSlide('fyne')">Desktop</button>
    <button onclick="showSlide('android')">Android</button>
  </div>
</div>
<script>
var carouselPlatforms = ['html', 'bubbletea', 'fyne', 'android'];
var carouselIndex = 0;
function showSlide(platform) {
  document.querySelectorAll('.carousel-slides .slide').forEach(function(s) { s.classList.remove('active'); });
  document.querySelectorAll('.carousel-tabs button').forEach(function(b) { b.classList.remove('active'); });
  var slide = document.querySelector('.slide[data-platform="' + platform + '"]');
  var btn = document.querySelector('.carousel-tabs button[onclick*="' + platform + '"]');
  if (slide) slide.classList.add('active');
  if (btn) btn.classList.add('active');
  carouselIndex = carouselPlatforms.indexOf(platform);
}
setInterval(function() {
  carouselIndex = (carouselIndex + 1) % carouselPlatforms.length;
  showSlide(carouselPlatforms[carouselIndex]);
}, 5000);
</script>
<style>
.platform-carousel { text-align: center; margin: 32px 0; }
.carousel-slides { position: relative; max-width: 800px; margin: 0 auto; }
.slide { display: none; }
.slide.active { display: block; }
.slide img { width: 100%; border-radius: 8px; box-shadow: 0 4px 16px rgba(0,0,0,0.15); }
.carousel-tabs { display: flex; justify-content: center; gap: 8px; margin-top: 16px; }
.carousel-tabs button { padding: 8px 20px; border: 1px solid #ddd; background: #f5f5f5; border-radius: 20px; cursor: pointer; font-size: 14px; transition: all 0.2s; }
.carousel-tabs button.active { background: #2196f3; color: #fff; border-color: #2196f3; }
.carousel-tabs button:hover { background: #e0e0e0; }
.carousel-tabs button.active:hover { background: #1976d2; }
</style>

It combines the reactivity of Svelte, the ergonomics of Vue, with a language and platform agnostic code generator. Tooling inspired by and built in Go.

## Design Philosophy

- **Declarative UI** — describe what your interface looks like, not how to build it
- **Reactive by default** — state changes automatically propagate to the UI
- **Cross-platform** — one source file targets Web, Desktop, Mobile, and Terminal
- **First-class expressions** — Go-like expressions appear directly in templates and compile to native logic in the target language
- **Compile-time analysis** — types, dependencies, and bindings are verified before code generation
- **Minimal runtime** — subscription-based updates with no virtual DOM diffing

## Quick Example

```sngl
struct Todo {
    text string = ""
    done bool = false
}

component main {
    var (newTodo = "", todos list<Todo> = [])
    computed status = "Todo List ({todos.length()} items)"

    vbox(style={gap=12, padding=16}) {
        text(value=status, style={fontWeight="bold", fontSize=24})
        hbox(style={gap=8, alignItems="center"}) {
            input(@input={ newTodo = event.value }, placeholder="Buy eggs",
                  style={flexGrow=1})
            button(@click={
                todos.push(Todo{text: newTodo, done: false})
                newTodo = ""
            }, text="Add")
        }
        vbox(style={gap=4}) {
            for item, index in todos {
                checkbox(checked=item.done, key=index, label=item.text,
                         @change={ todos[index].done!! })
            }
        }
    }
}
```

## Architecture

1. **Parser** — parses `.sngl` files into an AST
2. **Checker** — validates types, bindings, and dependency graphs
3. **Optimizer** — platform-specific AST transformations
4. **Code Generator** — pluggable backends emit target code

## Platforms

| Target   | Language   | Platform  | Status |
| -------- | ---------- | --------- | ------ |
| Web      | JavaScript | html      | Stable |
| Terminal | Go         | bubbletea | Stable |
| Desktop  | Go         | fyne      | Stable |
| Android  | Kotlin     | android   | Stable |

## Next Steps

- [Getting Started](getting-started/index.html) — learn the basics
- [Language Reference](language/reference.html) — complete language guide
- [Language Specification](language/specification.html) — formal grammar
