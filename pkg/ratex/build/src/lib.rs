// SPDX-License-Identifier: Unlicense OR MIT

//! A WebAssembly reactor over RaTeX: it lays a formula out and hands back
//! RaTeX's display list as JSON (docs/DISPLAYLIST_JSON_PROTOCOL.md in
//! RaTeX). The host writes the formula into memory it got from rt_alloc,
//! calls rt_layout, and reads rt_out_len bytes at rt_out.

use ratex_layout::{layout, to_display_list, LayoutOptions};
use ratex_parser::parse;
use ratex_types::color::Color;
use ratex_types::math_style::MathStyle;

static mut OUT: Vec<u8> = Vec::new();

/// rt_alloc returns len bytes for the host to write into.
#[no_mangle]
pub extern "C" fn rt_alloc(len: usize) -> *mut u8 {
    let mut v = Vec::<u8>::with_capacity(len);
    let p = v.as_mut_ptr();
    std::mem::forget(v);
    p
}

/// rt_free gives back what rt_alloc returned.
///
/// # Safety
/// p and len must be what rt_alloc took and returned.
#[no_mangle]
pub unsafe extern "C" fn rt_free(p: *mut u8, len: usize) {
    drop(Vec::from_raw_parts(p, 0, len));
}

/// rt_layout lays out the formula of len bytes of UTF-8 at p, in display
/// style when display is not 0, else in text style. It returns 0 when the
/// output is the display list as JSON, 1 when it is an error message.
///
/// The formula's own colour is transparent black: a host draws what has
/// no colour of its own (\color, \textcolor) in its text's colour.
///
/// # Safety
/// p must point at len readable bytes.
#[no_mangle]
pub unsafe extern "C" fn rt_layout(p: *const u8, len: usize, display: i32) -> i32 {
    let bytes = std::slice::from_raw_parts(p, len);
    let (code, out) = match std::str::from_utf8(bytes) {
        Err(e) => (1, format!("invalid UTF-8: {e}")),
        Ok(text) => match lay_out(text, display != 0) {
            Ok(json) => (0, json),
            Err(e) => (1, e),
        },
    };
    OUT = out.into_bytes();
    code
}

fn lay_out(text: &str, display: bool) -> Result<String, String> {
    let nodes = parse(text).map_err(|e| format!("{e}"))?;
    let style = if display { MathStyle::Display } else { MathStyle::Text };
    let options = LayoutOptions::default()
        .with_style(style)
        .with_color(Color::new(0.0, 0.0, 0.0, 0.0));
    let list = to_display_list(&layout(&nodes, &options));
    serde_json::to_string(&list).map_err(|e| format!("{e}"))
}

/// rt_out is where the output of the last rt_layout is.
#[no_mangle]
pub extern "C" fn rt_out() -> *const u8 {
    unsafe { (*std::ptr::addr_of!(OUT)).as_ptr() }
}

/// rt_out_len is how long the output of the last rt_layout is.
#[no_mangle]
pub extern "C" fn rt_out_len() -> usize {
    unsafe { (*std::ptr::addr_of!(OUT)).len() }
}
