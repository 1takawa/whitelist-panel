export default function Placeholder({ title, stage, text }: { title: string; stage: string; text: string }) {
  return (
    <section className="max-w-2xl">
      <h1 className="text-xl font-semibold">{title}</h1>
      <p className="mt-2 text-sm text-dim">{text}</p>
      <p className="mt-4 inline-block rounded-md border border-line bg-surface px-2.5 py-1 text-xs text-dim">
        {stage}
      </p>
    </section>
  )
}
