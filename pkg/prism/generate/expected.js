// Writes testdata/prismjs.json: how Prism.js 1.29.0 itself tokenizes the
// samples of testdata/samples.json, loaded as generate.js loads it. The
// package's tests compare its tokens with these. Run it as generate.js.

const fs = require('fs')
const path = require('path')
const Prism = require('prismjs')
const components = require('prismjs/components.js')

global.Prism = Prism

const loaded = {}
function loadLanguages(lngs) {
    for (const lng of (Array.isArray(lngs) ? lngs : (lngs ? [lngs] : []))) {
        loadLanguage(lng)
    }
}
function loadLanguage(lng) {
    if (!components.languages[lng] || !components.languages[lng].title) {
        return
    }
    loadLanguages(components.languages[lng].optional)
    loadLanguages(components.languages[lng].require)
    loadLanguages(components.languages[lng].modify)
    if (!loaded[lng]) {
        loaded[lng] = true
        require(`prismjs/components/prism-${lng}.js`)
    }
}
loadLanguages(Object.keys(components.languages))

// The innermost token's type and first alias, and the length in UTF-8 bytes,
// of each piece of text; neighbours alike are merged.
const encoder = new TextEncoder()
function flatten(stream, type, alias, out) {
    for (const t of (Array.isArray(stream) ? stream : [stream])) {
        if (typeof t === 'string') {
            const n = encoder.encode(t).length
            const last = out[out.length - 1]
            if (!n) {
                continue
            } else if (last && last[0] === type && last[1] === alias) {
                last[2] += n
            } else {
                out.push([type, alias, n])
            }
        } else {
            const a = Array.isArray(t.alias) ? (t.alias[0] || '') : (t.alias || '')
            flatten(t.content, t.type, a, out)
        }
    }
    return out
}

const testdata = path.join(__dirname, '..', 'testdata')
const samples = JSON.parse(fs.readFileSync(path.join(testdata, 'samples.json'), 'utf8'))
const expected = samples.map(([language, code]) => flatten(Prism.tokenize(code, Prism.languages[language]), '', '', []))
fs.writeFileSync(path.join(testdata, 'prismjs.json'), JSON.stringify(expected) + '\n')
