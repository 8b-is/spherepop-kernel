//! Independent SpherePOP Term, Rule & Trace Verifier in Pure Dependency-Free Rust
//! Implements SPEC.md Sections 2, 4, and 8 (including self-contained FIPS 180-4 SHA-256)
//! so that trace verification shares zero code with the Go reducer.

#![forbid(unsafe_code)]

const K256: [u32; 64] = [
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
    0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
    0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
];

pub fn sha256(data: &[u8]) -> [u8; 32] {
    let mut h: [u32; 8] = [
        0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
        0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
    ];
    let bit_len = (data.len() as u64) * 8;
    let mut msg = data.to_vec();
    msg.push(0x80);
    while (msg.len() % 64) != 56 {
        msg.push(0x00);
    }
    msg.extend_from_slice(&bit_len.to_be_bytes());

    for chunk in msg.chunks_exact(64) {
        let mut w = [0u32; 64];
        for i in 0..16 {
            w[i] = u32::from_be_bytes([
                chunk[i * 4],
                chunk[i * 4 + 1],
                chunk[i * 4 + 2],
                chunk[i * 4 + 3],
            ]);
        }
        for i in 16..64 {
            let s0 = w[i - 15].rotate_right(7) ^ w[i - 15].rotate_right(18) ^ (w[i - 15] >> 3);
            let s1 = w[i - 2].rotate_right(17) ^ w[i - 2].rotate_right(19) ^ (w[i - 2] >> 10);
            w[i] = w[i - 16]
                .wrapping_add(s0)
                .wrapping_add(w[i - 7])
                .wrapping_add(s1);
        }
        let (mut a, mut b, mut c, mut d, mut e, mut f, mut g, mut hh) =
            (h[0], h[1], h[2], h[3], h[4], h[5], h[6], h[7]);
        for i in 0..64 {
            let s1 = e.rotate_right(6) ^ e.rotate_right(11) ^ e.rotate_right(25);
            let ch = (e & f) ^ ((!e) & g);
            let temp1 = hh
                .wrapping_add(s1)
                .wrapping_add(ch)
                .wrapping_add(K256[i])
                .wrapping_add(w[i]);
            let s0 = a.rotate_right(2) ^ a.rotate_right(13) ^ a.rotate_right(22);
            let maj = (a & b) ^ (a & c) ^ (b & c);
            let temp2 = s0.wrapping_add(maj);

            hh = g;
            g = f;
            f = e;
            e = d.wrapping_add(temp1);
            d = c;
            c = b;
            b = a;
            a = temp1.wrapping_add(temp2);
        }
        h[0] = h[0].wrapping_add(a);
        h[1] = h[1].wrapping_add(b);
        h[2] = h[2].wrapping_add(c);
        h[3] = h[3].wrapping_add(d);
        h[4] = h[4].wrapping_add(e);
        h[5] = h[5].wrapping_add(f);
        h[6] = h[6].wrapping_add(g);
        h[7] = h[7].wrapping_add(hh);
    }

    let mut out = [0u8; 32];
    for (i, word) in h.iter().enumerate() {
        out[i * 4..i * 4 + 4].copy_from_slice(&word.to_be_bytes());
    }
    out
}

pub fn to_hex(bytes: &[u8; 32]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut s = String::with_capacity(64);
    for &b in bytes {
        s.push(HEX[(b >> 4) as usize] as char);
        s.push(HEX[(b & 0x0f) as usize] as char);
    }
    s
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[repr(u8)]
pub enum Kind {
    Atom = 1,
    Const = 2,
    Scope = 3,
    App = 4,
    Bracket = 5,
}

fn push_uvarint(buf: &mut Vec<u8>, mut v: u64) {
    while v >= 0x80 {
        buf.push((v as u8) | 0x80);
        v >>= 7;
    }
    buf.push(v as u8);
}

fn push_string(buf: &mut Vec<u8>, s: &str) {
    push_uvarint(buf, s.len() as u64);
    buf.extend_from_slice(s.as_bytes());
}

const TERM_DOMAIN: &str = "spherepop-term/1";
const CONFIG_DOMAIN: &str = "spherepop-config/1";
const TRACE_FORMAT: &str = "spherepop-trace/1";

pub fn digest_leaf(kind: Kind, name: &str) -> [u8; 32] {
    let mut buf = Vec::with_capacity(32 + name.len());
    push_string(&mut buf, TERM_DOMAIN);
    buf.push(kind as u8);
    push_string(&mut buf, name);
    sha256(&buf)
}

pub fn digest_app(f: &[u8; 32], x: &[u8; 32]) -> [u8; 32] {
    let mut buf = Vec::with_capacity(84);
    push_string(&mut buf, TERM_DOMAIN);
    buf.push(Kind::App as u8);
    buf.extend_from_slice(f);
    buf.extend_from_slice(x);
    sha256(&buf)
}

pub fn digest_bracket(label: &str, body: &[u8; 32]) -> [u8; 32] {
    let mut buf = Vec::with_capacity(64 + label.len());
    push_string(&mut buf, TERM_DOMAIN);
    buf.push(Kind::Bracket as u8);
    push_string(&mut buf, label);
    buf.extend_from_slice(body);
    sha256(&buf)
}

pub fn sum_zero_separated(parts: &[&[u8]]) -> String {
    let mut buf = Vec::new();
    for p in parts {
        buf.extend_from_slice(p);
        buf.push(0);
    }
    to_hex(&sha256(&buf))
}

pub fn config_digest(canonical_config_json: &str) -> String {
    sum_zero_separated(&[CONFIG_DOMAIN.as_bytes(), canonical_config_json.as_bytes()])
}

pub fn trace_genesis(start_digest_hex: &str, cfg_digest_hex: &str) -> String {
    sum_zero_separated(&[
        TRACE_FORMAT.as_bytes(),
        start_digest_hex.as_bytes(),
        cfg_digest_hex.as_bytes(),
    ])
}

pub fn trace_link(prev_hex: &str, rule: &str, path: &str, after_hex: &str) -> String {
    sum_zero_separated(&[
        prev_hex.as_bytes(),
        rule.as_bytes(),
        path.as_bytes(),
        after_hex.as_bytes(),
    ])
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct StepClaim {
    pub rule: String,
    pub path: String,
    pub after: String,
    pub link: String,
}

pub fn verify_trace_chain(
    canonical_config_json: &str,
    start_digest: &str,
    steps: &[StepClaim],
    final_digest: &str,
    claimed_head: &str,
) -> Result<(), String> {
    let cfg_d = config_digest(canonical_config_json);
    let mut prev = trace_genesis(start_digest, &cfg_d);
    for (idx, step) in steps.iter().enumerate() {
        let expected_link = trace_link(&prev, &step.rule, &step.path, &step.after);
        if expected_link != step.link {
            return Err(format!(
                "step {} link mismatch: expected {}, got {}",
                idx, expected_link, step.link
            ));
        }
        prev = expected_link;
    }
    if prev != claimed_head {
        return Err(format!("head mismatch: expected {}, got {}", prev, claimed_head));
    }
    if let Some(last) = steps.last() {
        if last.after != final_digest {
            return Err(format!(
                "final_digest mismatch: last step after={} != final_digest={}",
                last.after, final_digest
            ));
        }
    } else if start_digest != final_digest {
        return Err("0-step trace must have start_digest == final_digest".to_string());
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn verify_skk_x_digests_and_trace_chain() {
        let s = digest_leaf(Kind::Const, "S");
        let k = digest_leaf(Kind::Const, "K");
        let x = digest_leaf(Kind::Atom, "x");

        let sk = digest_app(&s, &k);
        let skk = digest_app(&sk, &k);
        let start = digest_app(&skk, &x);
        let start_hex = to_hex(&start);
        assert_eq!(
            start_hex,
            "381945db076b66348394c22dba6a47677b3af80e056b7665d59ad12fc304aa83"
        );

        let kx = digest_app(&k, &x);
        let step1 = digest_app(&kx, &kx);
        let step1_hex = to_hex(&step1);
        assert_eq!(
            step1_hex,
            "a920c3a3388f4eeceb265437e40957f5000b1325616ddbd1f230e9e2c5a36ab2"
        );

        let final_hex = to_hex(&x);
        assert_eq!(
            final_hex,
            "07685251c255bb056e562da8ea71975e722286dc4f4d243210b102adbd3c1395"
        );

        let cfg_json =
            r#"{"root":["W","K","C","S"],"gated":["W","K","C"],"absorb":"u","sc":"any"}"#;
        let steps = vec![
            StepClaim {
                rule: "S".to_string(),
                path: "".to_string(),
                after: step1_hex.clone(),
                link: "74cfcedff61e9217d34102cce107fb59d0657f96f1402df29c701f319827bc27"
                    .to_string(),
            },
            StepClaim {
                rule: "K".to_string(),
                path: "".to_string(),
                after: final_hex.clone(),
                link: "5c8dd5ef186dece361588fd37c45305dc52b15094f41ed920b836c0e1abb1109"
                    .to_string(),
            },
        ];

        assert!(verify_trace_chain(
            cfg_json,
            &start_hex,
            &steps,
            &final_hex,
            "5c8dd5ef186dece361588fd37c45305dc52b15094f41ed920b836c0e1abb1109"
        )
        .is_ok());

        // Negative control: tampered link must be rejected
        let mut tampered = steps.clone();
        tampered[0].link =
            "0000000000000000000000000000000000000000000000000000000000000000".to_string();
        assert!(verify_trace_chain(
            cfg_json,
            &start_hex,
            &tampered,
            &final_hex,
            "5c8dd5ef186dece361588fd37c45305dc52b15094f41ed920b836c0e1abb1109"
        )
        .is_err());
    }
}
