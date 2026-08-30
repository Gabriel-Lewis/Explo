import { useRef, useState, useEffect, useCallback } from 'react'

/*
 * Quality range: which formats are acceptable, and how big a file may be.
 *
 * The bitrate scale is non-linear. Real bitrates run from 128 to about 4600
 * kbps, so evenly spaced stops keep the lossy end — the part most people
 * actually tune — from collapsing into the first few percent of the track.
 *
 * The top stop means "no limit" rather than 4608 kbps, and writes 0, which is
 * what disables the ceiling in config.
 */

export const BITRATE_STOPS = [128, 192, 256, 320, 700, 1000, 1411, 0]

const STOP_LABELS = [
  { label: '128' },
  { label: '192' },
  { label: '256' },
  { label: '320', note: 'mp3 max' },
  { label: '700' },
  { label: '1000', note: 'flac cd' },
  { label: '1411', note: 'wav cd' },
  { label: 'No limit' },
]

// Index of the highest stop reachable with lossy formats alone.
const LOSSY_CEILING = 3

export const FORMATS = [
  { ext: 'flac', name: 'FLAC', lossless: true, size: '20–100 MB' },
  { ext: 'mp3', name: 'MP3', lossless: false, size: '5–10 MB' },
  { ext: 'm4a', name: 'AAC / M4A', lossless: false, size: '7–9 MB' },
  { ext: 'wav', name: 'WAV', lossless: true, size: '40–140 MB' },
]

const KNOWN = new Set(FORMATS.map(f => f.ext))

/** Extensions the checkboxes do not cover, so they survive a round trip. */
export function extraExtensions(extensions) {
  return splitExtensions(extensions).filter(e => !KNOWN.has(e))
}

function splitExtensions(extensions) {
  return (extensions || '')
    .split(',')
    .map(e => e.trim().toLowerCase())
    .filter(Boolean)
}

/** Nearest stop index for a configured bitrate. 0 (or absent) means no limit. */
export function stopIndexFor(bitrate, fallback) {
  if (bitrate === 0) return BITRATE_STOPS.length - 1
  if (!bitrate) return fallback
  let best = fallback
  let bestGap = Infinity
  BITRATE_STOPS.forEach((stop, i) => {
    if (stop === 0) return
    const gap = Math.abs(stop - bitrate)
    if (gap < bestGap) { bestGap = gap; best = i }
  })
  return best
}

export const PREFERENCES = [
  { value: 'smaller', label: 'Prefer smaller' },
  { value: 'none', label: 'No preference' },
  { value: 'larger', label: 'Prefer larger' },
]

const PREFERENCE_HINTS = {
  smaller: 'Takes the best lossy file it can find, and only falls back to lossless when there is none.',
  none: 'Order of the formats above decides, as it always has.',
  larger: 'Takes the best lossless file it can find, and falls back to lossy when there is none.',
}

export default function QualityRange({ extensions, minBitrate, maxBitrate, sizePreference, onChange, isLocked }) {
  const trackRef = useRef(null)
  const [drag, setDrag] = useState(null)

  const selected = splitExtensions(extensions)
  const extras = extraExtensions(extensions)
  const hasLossless = FORMATS.some(f => f.lossless && selected.includes(f.ext))
  const cap = hasLossless ? BITRATE_STOPS.length - 1 : LOSSY_CEILING

  const lo = Math.min(stopIndexFor(minBitrate, 2), cap)
  const hi = Math.min(stopIndexFor(maxBitrate === undefined ? 0 : maxBitrate, BITRATE_STOPS.length - 1), cap)

  const emit = useCallback((next) => {
    onChange({
      extensions: next.extensions ?? extensions,
      minBitrate: next.lo === undefined ? minBitrate : BITRATE_STOPS[next.lo] || 128,
      maxBitrate: next.hi === undefined ? maxBitrate : BITRATE_STOPS[next.hi],
      sizePreference: next.sizePreference ?? sizePreference,
    })
  }, [extensions, minBitrate, maxBitrate, sizePreference, onChange])

  function toggleFormat(ext) {
    if (isLocked) return
    const has = selected.includes(ext)
    // Keep the configured order, and never drop extensions we do not show.
    const next = has ? selected.filter(e => e !== ext) : [...selected, ext]
    emit({ extensions: next.join(',') })
  }

  const pct = i => (i / (BITRATE_STOPS.length - 1)) * 100

  const nearest = useCallback(e => {
    const rect = trackRef.current.getBoundingClientRect()
    const raw = ((e.clientX - rect.left) / rect.width) * (BITRATE_STOPS.length - 1)
    return Math.max(0, Math.min(cap, Math.round(raw)))
  }, [cap])

  useEffect(() => {
    if (!drag) return
    const move = e => {
      const i = nearest(e)
      if (drag === 'lo') emit({ lo: Math.min(i, hi) })
      else emit({ hi: Math.max(i, lo) })
    }
    const up = () => setDrag(null)
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
    return () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
    }
  }, [drag, nearest, emit, lo, hi])

  const handleStyle = i => ({
    position: 'absolute', top: '50%', left: `${pct(i)}%`,
    width: 16, height: 16, margin: '-8px 0 0 -8px', borderRadius: '50%',
    background: isLocked ? '#4a4a4a' : '#fff',
    cursor: isLocked ? 'not-allowed' : 'grab',
    boxShadow: '0 1px 4px rgba(0,0,0,.6)', touchAction: 'none',
  })

  const ceiling = BITRATE_STOPS[hi]

  return (
    <div className="flex flex-col gap-4">
      <div>
        <p className="text-[13px] font-medium mb-0.5">Accepted formats</p>
        <p className="text-[11.5px] text-muted mb-2">
          Only these are considered.{' '}
          {(sizePreference || 'none') === 'none'
            ? 'With no preference set below, their order in the config file decides which is tried first.'
            : 'Which is tried first is decided by the preference below, not by their order.'}
        </p>
        <div className="flex flex-col gap-px">
          {FORMATS.map(f => (
            <label
              key={f.ext}
              className={`flex items-center gap-2.5 px-2.5 py-2 rounded-[6px] select-none
                ${isLocked ? 'opacity-45 cursor-not-allowed' : 'cursor-pointer hover:bg-[#202020]'}`}
            >
              <input
                type="checkbox"
                checked={selected.includes(f.ext)}
                onChange={() => toggleFormat(f.ext)}
                disabled={isLocked}
                style={{ accentColor: '#1ed760', margin: 0, width: 15, height: 15 }}
              />
              <span className="flex-1 text-[13px]">{f.name}</span>
              <span className="text-[10px] text-muted border border-ui-border rounded-[3px] px-1.5 uppercase tracking-wide">
                {f.lossless ? 'lossless' : 'lossy'}
              </span>
              <span className="text-[11px] text-muted tabular-nums min-w-[78px] text-right">{f.size}</span>
            </label>
          ))}
        </div>
        {extras.length > 0 && (
          <p className="text-[11px] text-muted mt-2 pl-2.5">
            Also accepting <span className="text-white">{extras.join(', ')}</span> from your config file.
          </p>
        )}
      </div>

      <div>
        <p className="text-[13px] font-medium mb-0.5">Bitrate range</p>
        <p className="text-[11.5px] text-muted mb-2">
          Worked out from file size when a peer reports no bitrate, so it applies to everything.
        </p>

        <div className="pt-6 px-2">
          <div
            ref={trackRef}
            className="relative h-1 bg-well rounded-[2px]"
            onPointerDown={e => {
              if (isLocked || e.target.dataset.handle) return
              const i = nearest(e)
              if (Math.abs(i - lo) <= Math.abs(i - hi)) emit({ lo: Math.min(i, hi) })
              else emit({ hi: Math.max(i, lo) })
            }}
          >
            <div
              className="absolute h-full bg-accent rounded-[2px]"
              style={{ left: `${pct(lo)}%`, width: `${pct(hi) - pct(lo)}%` }}
            />
            <div data-handle="lo" style={handleStyle(lo)}
              onPointerDown={e => { if (!isLocked) { setDrag('lo'); e.preventDefault() } }} />
            <div data-handle="hi" style={handleStyle(hi)}
              onPointerDown={e => { if (!isLocked) { setDrag('hi'); e.preventDefault() } }} />
          </div>

          <div className="relative h-8 mt-3">
            {STOP_LABELS.map((s, i) => (
              <span
                key={s.label}
                className={`absolute -translate-x-1/2 text-[10px] text-center leading-tight whitespace-nowrap
                  ${i >= lo && i <= hi ? 'text-accent' : 'text-muted'}`}
                style={{ left: `${pct(i)}%`, opacity: i > cap ? 0.35 : 1 }}
              >
                {s.label}
                {s.note && <em className="block not-italic text-[9px] text-muted mt-px">{s.note}</em>}
              </span>
            ))}
          </div>
        </div>

        <p className="text-[12px] text-muted mt-1.5">
          Accepting <span className="text-white">{BITRATE_STOPS[lo] || 128} kbps</span>
          {' to '}
          <span className="text-white">{ceiling ? `${ceiling} kbps` : 'no limit'}</span>.
          {!hasLossless && ' Lossless is off, so the ceiling stops at 320 kbps.'}
          {selected.length === 0 && (
            <span className="text-danger"> No formats selected — every file would be rejected.</span>
          )}
        </p>
      </div>

      <div>
        <p className="text-[13px] font-medium mb-0.5">Within that range, reach for</p>
        <p className="text-[11.5px] text-muted mb-2">
          This orders candidates; it never rejects one. Whichever end you prefer, the best file of
          that kind wins — preferring smaller still takes a 320 over a 256.
        </p>
        <div className="flex gap-1">
          {PREFERENCES.map(p => {
            const active = (sizePreference || 'none') === p.value
            return (
              <button
                key={p.value}
                type="button"
                disabled={isLocked}
                onClick={() => emit({ sizePreference: p.value })}
                className={`flex-1 rounded-[6px] border px-2 py-2.5 text-[12px] transition-colors
                  ${active
                    ? 'border-accent bg-[#17492c] text-white'
                    : 'border-ui-border bg-well text-muted hover:border-[#3a3a3a]'}
                  ${isLocked ? 'opacity-45 cursor-not-allowed' : 'cursor-pointer'}`}
              >
                {p.label}
              </button>
            )
          })}
        </div>
        <p className="text-[11.5px] text-muted mt-2">
          {PREFERENCE_HINTS[sizePreference || 'none']}
        </p>
      </div>
    </div>
  )
}
