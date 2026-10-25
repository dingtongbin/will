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

    let mut samples = Vec::with_capacity(REPS * COUNT as usize);
    let mut rounds = Vec::with_capacity(REPS);
    for _ in 0..REPS {
        let round = Instant::now();
        let mut handles = Vec::with_capacity(COUNT as usize);
        for i in 0..COUNT {
            let start = Instant::now();
            handles.push(tokio::spawn(async move {
                black_box(work(i));
                start.elapsed().as_nanos()
            }));
        }
        let mut checksum = 0i64;
        for handle in handles {
            let latency = handle.await.unwrap();
            samples.push(latency);
            checksum = checksum.wrapping_add(latency as i64);
        }
        rounds.push(round.elapsed().as_nanos());
        black_box(checksum);
    }
    samples.sort_unstable();
    let p50 = samples[(samples.len() - 1) * 50 / 100];
    let p99 = samples[(samples.len() - 1) * 99 / 100];
    let p999 = samples[(samples.len() - 1) * 999 / 1000];
    let elapsed: u128 = rounds.iter().sum();
    let cores = std::thread::available_parallelism()
        .map(|value| value.get() as u128)
        .unwrap_or(1);
    let ops_per_sec = (REPS * COUNT as usize) as u128 * 1_000_000_000u128 / elapsed.max(1);
    println!("spawn p50 {p50} ns");
    println!("spawn p99 {p99} ns");
    println!("spawn p999 {p999} ns");
    println!("spawn tasks {COUNT}");
    println!("spawn ops_per_sec {ops_per_sec}");
    println!("spawn ops_per_core_sec {}", ops_per_sec / cores);
}
