import { ButtonHTMLAttributes, ReactNode } from "react";
import { Link, LinkProps } from "react-router-dom";

export type ButtonVariant = "primary" | "secondary" | "danger" | "warning" | "ghost";
export type ButtonSize = "sm" | "md";

type CommonProps = {
  variant?: ButtonVariant;
  size?: ButtonSize;
  children: ReactNode;
};

/**
 * Button — single source of truth for button styling. Drop the
 * hand-rolled `className="rounded bg-accent px-3 py-2 text-sm…"`
 * triplets and reach for `<Button variant="primary">`.
 *
 * Variants:
 *   primary    — the main affirmative action on a form (one per surface)
 *   secondary  — outlined; for non-destructive alternatives
 *   danger     — destructive action; pairs with the danger callout/tone
 *   warning    — caution-tinted; for reopen / unlock kinds of actions
 *   ghost      — text-only; for "Cancel" or low-prominence inline ops
 */
export function Button({
  variant = "primary",
  size = "md",
  className = "",
  children,
  ...rest
}: CommonProps & ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button className={`${baseClass(variant, size)} ${className}`} {...rest}>
      {children}
    </button>
  );
}

/**
 * ButtonLink — react-router <Link/> that looks like a button. Same
 * variants/sizes as Button so the eye can't tell the two apart.
 */
export function ButtonLink({
  variant = "primary",
  size = "md",
  className = "",
  children,
  ...rest
}: CommonProps & LinkProps) {
  return (
    <Link className={`${baseClass(variant, size)} inline-flex items-center ${className}`} {...rest}>
      {children}
    </Link>
  );
}

function baseClass(variant: ButtonVariant, size: ButtonSize): string {
  const sizing = size === "sm" ? "px-2.5 py-1 text-xs" : "px-3 py-2 text-sm";
  const shape = "rounded font-medium transition-colors disabled:opacity-50 disabled:cursor-not-allowed";
  return `${shape} ${sizing} ${variantClass(variant)}`;
}

function variantClass(v: ButtonVariant): string {
  switch (v) {
    case "secondary":
      return "border border-border-strong bg-surface-2 text-fg hover:bg-surface-3";
    case "danger":
      return "bg-danger text-white hover:bg-danger/80";
    case "warning":
      return "border border-warning/40 bg-warning/10 text-warning-fg hover:bg-warning/20";
    case "ghost":
      return "text-fg-muted hover:bg-surface-3 hover:text-fg";
    default:
      return "bg-accent text-accent-fg hover:bg-accent-hover";
  }
}
