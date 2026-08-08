import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode } from 'react'

/** A short, quiet loading line. */
export function Spinner({ label }: { label: string }) {
  return (
    <p role="status" className="text-sm text-slate-500">
      {label}…
    </p>
  )
}

/** A failure the user has to read. */
export function Alert({ children }: { children: ReactNode }) {
  return (
    <p role="alert" className="rounded border border-rose-200 bg-rose-50 px-3 py-2 text-sm text-rose-800">
      {children}
    </p>
  )
}

/** A confirmation. */
export function Notice({ children }: { children: ReactNode }) {
  return (
    <p role="status" className="rounded border border-emerald-200 bg-emerald-50 px-3 py-2 text-sm text-emerald-800">
      {children}
    </p>
  )
}

export function Button({ className = '', ...props }: ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      {...props}
      className={
        'rounded bg-slate-900 px-3 py-2 text-sm font-medium text-white disabled:cursor-not-allowed ' +
        'disabled:opacity-50 hover:bg-slate-700 ' +
        className
      }
    />
  )
}

export function SecondaryButton({ className = '', ...props }: ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      {...props}
      className={
        'rounded border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 ' +
        'disabled:opacity-50 hover:bg-slate-50 ' +
        className
      }
    />
  )
}

export function TextInput({ className = '', ...props }: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      {...props}
      className={
        'w-full rounded border border-slate-300 bg-white px-2 py-1.5 text-sm ' +
        'focus:border-slate-500 focus:outline-none ' +
        className
      }
    />
  )
}

/** A panel with a heading. */
export function Card({ title, children }: { title?: string; children: ReactNode }) {
  return (
    <section className="rounded-lg border border-slate-200 bg-white p-5">
      {title !== undefined && <h2 className="mb-4 text-sm font-semibold text-slate-700">{title}</h2>}
      {children}
    </section>
  )
}
