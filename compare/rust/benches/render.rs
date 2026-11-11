use criterion::{criterion_group, criterion_main, Criterion, Throughput};
use sailfish::runtime::Buffer;
use sailfish_compare::cases;
use std::hint::black_box;
use std::time::Duration;

fn fnv1a(data: &[u8]) -> u64 {
    let mut hash: u64 = 0xcbf29ce484222325;
    for byte in data {
        hash ^= *byte as u64;
        hash = hash.wrapping_mul(0x100000001b3);
    }
    hash
}

fn render_benchmarks(c: &mut Criterion) {
    for case in cases() {
        let values: &[&str] = &case.values;
        let rendered = (case.render)(values);
        assert_eq!(rendered.len(), case.expected_len, "{} length", case.name);
        assert_eq!(fnv1a(rendered.as_bytes()), case.expected_hash, "{} content", case.name);

        let mut group = c.benchmark_group(case.name);
        group.throughput(Throughput::Bytes(case.expected_len as u64));
        group.bench_function("sailfish_string", |b| {
            b.iter(|| (case.render)(black_box(values)))
        });
        let mut buf = Buffer::new();
        group.bench_function("sailfish_buffer", |b| {
            b.iter(|| {
                buf.clear();
                (case.render_to)(black_box(values), &mut buf);
                black_box(buf.len())
            })
        });
        group.finish();
    }
}

criterion_group! {
    name = benches;
    config = Criterion::default()
        .warm_up_time(Duration::from_millis(500))
        .measurement_time(Duration::from_secs(2));
    targets = render_benchmarks
}
criterion_main!(benches);
