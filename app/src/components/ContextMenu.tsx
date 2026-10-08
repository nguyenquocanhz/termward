import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { create } from "zustand";

export type ContextItem =
  | { separator: true }
  | {
      separator?: false;
      label: string;
      icon?: ReactNode;
      /** Shortcut shown on the right. */
      hint?: string;
      danger?: boolean;
      disabled?: boolean;
      run: () => void;
    };

interface OpenMenu {
  id: number;
  x: number;
  y: number;
  items: ContextItem[];
  label?: string;
  restore: () => void;
}

const useMenu = create<{ menu: OpenMenu | null }>(() => ({ menu: null }));
let seq = 0;

interface PointerLike {
  clientX: number;
  clientY: number;
  preventDefault?: () => void;
  currentTarget?: EventTarget | null;
}

/**
 * Opens the app's context menu at the pointer. Focus goes back to whatever
 * had it (or to `restoreFocus`, e.g. the terminal) when the menu closes.
 */
export function openContextMenu(
  at: PointerLike,
  items: ContextItem[],
  opts: { label?: string; restoreFocus?: () => void } = {},
) {
  at.preventDefault?.();
  let { clientX: x, clientY: y } = at;
  // Opened from the keyboard (Menu key, Shift+F10): there is no pointer.
  if (x === 0 && y === 0 && at.currentTarget instanceof HTMLElement) {
    const r = at.currentTarget.getBoundingClientRect();
    x = r.left + Math.min(r.width / 2, 24);
    y = r.top + r.height / 2;
  }
  const before = document.activeElement;
  const restore =
    opts.restoreFocus ??
    (() => {
      if (before instanceof HTMLElement && before.isConnected) before.focus();
    });
  useMenu.setState({ menu: { id: ++seq, x, y, items, label: opts.label, restore } });
}

export function closeContextMenu() {
  const m = useMenu.getState().menu;
  if (!m) return;
  useMenu.setState({ menu: null });
  try {
    m.restore();
  } catch {
    /* what had focus is gone (a closed tab) */
  }
}

/** Mounted once; renders whichever menu is open. */
export function ContextMenuHost() {
  const menu = useMenu((s) => s.menu);
  return menu ? <MenuList key={menu.id} menu={menu} /> : null;
}

const MARGIN = 8;

function MenuList({ menu }: { menu: OpenMenu }) {
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null);

  // Keep the whole menu on screen: shift left at the right edge, open upwards
  // near the bottom.
  useLayoutEffect(() => {
    const el = ref.current!;
    const { width, height } = el.getBoundingClientRect();
    const vw = window.innerWidth;
    const vh = window.innerHeight;
    const left = Math.max(MARGIN, Math.min(menu.x, vw - width - MARGIN));
    const top =
      menu.y + height + MARGIN <= vh ? menu.y : Math.max(MARGIN, Math.min(menu.y - height, vh - height - MARGIN));
    setPos({ left, top });
    // Focus moves into the menu at once, so the keys below drive it and
    // nothing typed reaches the terminal behind.
    (enabled(el)[0] ?? el).focus({ preventScroll: true });
  }, [menu]);

  useEffect(() => {
    const inside = (e: Event) => e.target instanceof Node && !!ref.current?.contains(e.target);
    const onDown = (e: Event) => !inside(e) && closeContextMenu();
    const onScroll = (e: Event) => {
      // A terminal scrolls by itself whenever output arrives: that is not the
      // user moving the page away from under the menu.
      if (inside(e) || (e.target instanceof Element && e.target.closest(".term-stage"))) return;
      closeContextMenu();
    };
    window.addEventListener("pointerdown", onDown, true);
    window.addEventListener("wheel", onDown, { capture: true, passive: true });
    window.addEventListener("scroll", onScroll, true);
    window.addEventListener("resize", closeContextMenu);
    window.addEventListener("blur", closeContextMenu);
    return () => {
      window.removeEventListener("pointerdown", onDown, true);
      window.removeEventListener("wheel", onDown, true);
      window.removeEventListener("scroll", onScroll, true);
      window.removeEventListener("resize", closeContextMenu);
      window.removeEventListener("blur", closeContextMenu);
    };
  }, []);

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const items = enabled(ref.current!);
    const at = items.indexOf(document.activeElement as HTMLButtonElement);
    let next: HTMLButtonElement | undefined;
    switch (e.key) {
      case "ArrowDown":
        next = items[(at + 1) % items.length];
        break;
      case "ArrowUp":
        next = items[(at - 1 + items.length) % items.length];
        break;
      case "Home":
      case "PageUp":
        next = items[0];
        break;
      case "End":
      case "PageDown":
        next = items[items.length - 1];
        break;
      case "Escape":
      case "Tab":
        closeContextMenu();
        break;
      case "Enter":
      case " ":
        // The focused button handles these itself; just keep them from
        // reaching the page behind.
        e.stopPropagation();
        return;
      default:
        e.stopPropagation();
        return;
    }
    e.preventDefault();
    e.stopPropagation();
    next?.focus();
  };

  return (
    <div
      ref={ref}
      className="menu-list ctx-menu"
      role="menu"
      aria-label={menu.label}
      aria-orientation="vertical"
      tabIndex={-1}
      // Not `visibility: hidden` while it is measured: that would refuse focus.
      style={pos ? pos : { left: 0, top: 0, opacity: 0, pointerEvents: "none" }}
      onKeyDown={onKeyDown}
      onContextMenu={(e) => e.preventDefault()}
    >
      {menu.items.map((it, i) =>
        it.separator ? (
          <div key={i} className="menu-sep" role="separator" />
        ) : (
          <button
            key={i}
            type="button"
            role="menuitem"
            tabIndex={-1}
            disabled={it.disabled}
            aria-disabled={it.disabled || undefined}
            className={`menu-item${it.danger ? " danger" : ""}`}
            onMouseEnter={(e) => !it.disabled && e.currentTarget.focus({ preventScroll: true })}
            onClick={() => {
              closeContextMenu();
              it.run();
            }}
          >
            {it.icon ?? <span className="menu-noicon" />}
            <span className="menu-label">{it.label}</span>
            {it.hint && <span className="menu-hint">{it.hint}</span>}
          </button>
        ),
      )}
    </div>
  );
}

function enabled(root: HTMLElement): HTMLButtonElement[] {
  return [...root.querySelectorAll<HTMLButtonElement>('[role="menuitem"]:not(:disabled)')];
}
