use std::hint::black_box;
use std::time::Instant;

fn work(index: i32) -> i32 {
    let mut acc = 0i32;
    for i in 0..100 {
        acc += i;
    }
    acc + index
}

#[tokio::main(flavor = "multi_thread")]
async fn main() {
    const COUNT: i32 = 10_000;
    const REPS: usize = 5;

    let warm = tokio::spawn(async { work(0) });
    black_box(warm.await.unwrap());

    let mut samples = Vec::with_capacity(REPS);
    for _ in 0..REPS {
        let start = Instant::now();
        let mut handles = Vec::with_capacity(COUNT as usize);
        for i in 0..COUNT {
            handles.push(tokio::spawn(async move { work(i) }));
        }
        let mut checksum = 0i64;
        for handle in handles {
            checksum = checksum.wrapping_add(i64::from(handle.await.unwrap()));
        }
        samples.push(start.elapsed().as_nanos());
        black_box(checksum);
    }
    samples.sort_unstable();
    let p50 = samples[samples.len() / 2];
    let p95 = samples[samples.len() * 95 / 100];
    println!("spawn p50 {p50} ns");
    println!("spawn p95 {p95} ns");
    println!("spawn tasks {COUNT}");
}
