import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode } from 'react'

export function Spinner({ label }: { label: string }) {
  return (
    <p role="status" className="text-sm text-stone-500">
      {label}…
    </p>
  )
}

export function Alert({ children }: { children: ReactNode }) {
  return (
    <p role="alert" className="rounded border border-rose-200 bg-rose-50 px-3 py-2 text-sm text-rose-800">
      {children}
    </p>
  )
}

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
        'rounded bg-stone-900 px-3 py-2 text-sm font-medium text-white hover:bg-stone-700 ' +
        'disabled:cursor-not-allowed disabled:opacity-50 ' +
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
        'rounded border border-stone-300 bg-white px-3 py-2 text-sm font-medium text-stone-700 ' +
        'hover:bg-stone-50 disabled:opacity-50 ' +
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
        'w-full rounded border border-stone-300 bg-white px-2 py-1.5 text-sm ' +
        'focus:border-stone-500 focus:outline-none ' +
        className
      }
    />
  )
}

export function Card({ title, children }: { title?: string; children: ReactNode }) {
  return (
    <section className="rounded-lg border border-stone-200 bg-white p-5">
      {title !== undefined && <h2 className="mb-4 text-sm font-semibold text-stone-700">{title}</h2>}
      {children}
    </section>
  )
}

/** A number the SERVICE derived. Marked so a reader knows nothing here made it. */
export function Derived({ children, title }: { children: ReactNode; title?: string }) {
  return (
    <span
      title={title ?? 'Derived by flexitype'}
      className="rounded bg-stone-100 px-1.5 py-0.5 font-mono text-[13px] text-stone-800"
    >
      {children}
    </span>
  )
}
