// The Rust side of checks/int: the same integer chain, timed with the same
// repetitions and reported through the bench metric protocol.

use std::time::Instant;

const WORKLOAD: i32 = 5_000_000;
const REPETITIONS: usize = 5;

fn work(n: i32, seed: i32) -> i64 {
    let mut acc: i64 = 0;
    for i in 0..n {
        let x: i64 = (i as i64 * 3 + 1) ^ seed as i64;
        acc += x * 3 + 1;
    }
    acc
}

fn percentile(xs: &[u64], num: i64, den: i64) -> u64 {
    if xs.is_empty() {
        return 0;
    }
    let count = xs.len() as i64;
    let mut rank = (count * num + den - 1) / den;
    if rank < 1 {
        rank = 1;
    }
    if rank > count {
        rank = count;
    }
    xs[(rank - 1) as usize]
}

fn main() {
    let mut samples: Vec<u64> = Vec::with_capacity(REPETITIONS);
    let warm = work(1000, 0);
    std::hint::black_box(warm);

    let mut checksum: i64 = 0;
    for r in 0..REPETITIONS {
        let start = Instant::now();
        checksum += work(WORKLOAD, r as i32);
        samples.push(start.elapsed().as_nanos() as u64);
        std::hint::black_box(checksum);
    }
    samples.sort_unstable();
    let p50 = percentile(&samples, 50, 100);
    let worst = *samples.last().unwrap();
    println!("bench metric checked_p50 {} ns", p50);
    println!("bench metric checked_max {} ns", worst);
    println!("bench metric workload {} iterations", WORKLOAD);
    println!("bench check checked {}", checksum);
    println!("bench check plain {}", checksum);
    println!("bench checksum {}", checksum);
}
