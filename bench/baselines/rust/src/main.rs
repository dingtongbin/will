use std::hint::black_box;
use std::time::Instant;

use crossbeam_channel::{bounded, select};

fn percentile(samples: &mut [u128], percent: usize) -> u128 {
    samples.sort_unstable();
    let index = (samples.len() - 1) * percent / 100;
    samples[index]
}

fn report(name: &str, unit: &str, mut samples: Vec<u128>, count: u64) {
    let p50 = percentile(&mut samples, 50);
    let p95 = percentile(&mut samples, 95);
    let p99 = percentile(&mut samples, 99);
    println!("{name} p50 {p50} ns");
    println!("{name} p95 {p95} ns");
    println!("{name} p99 {p99} ns");
    println!("{name} {unit} {count}");
}

fn chan() {
    const COUNT: u64 = 1_000_000;
    const REPS: usize = 5;
    let (sender, receiver) = bounded::<i64>(64);
    let mut warm = 0i64;
    for i in 0..1000i64 {
        sender.send(i).unwrap();
        warm = warm.wrapping_add(receiver.recv().unwrap());
    }
    black_box(warm);

    let mut samples = Vec::with_capacity(REPS);
    let mut checksum = 0i64;
    for _ in 0..REPS {
        let start = Instant::now();
        let mut total = 0i64;
        for i in 0..COUNT as i64 {
            sender.send(i).unwrap();
            total = total.wrapping_add(receiver.recv().unwrap());
        }
        samples.push(start.elapsed().as_nanos());
        checksum = checksum.wrapping_add(total);
        black_box(checksum);
    }
    report("chan", "messages", samples, COUNT);
}

fn choose() {
    const COUNT: u64 = 500_000;
    const REPS: usize = 5;
    let (first_tx, first_rx) = bounded::<i64>(1);
    let (_second_tx, second_rx) = bounded::<i64>(1);
    let (_third_tx, third_rx) = bounded::<i64>(1);
    let (_fourth_tx, fourth_rx) = bounded::<i64>(1);

    let mut warm = 0i64;
    for i in 0..1000i64 {
        first_tx.send(i).unwrap();
        select! {
            recv(first_rx) -> value => { if let Ok(v) = value { warm = warm.wrapping_add(v); } }
            recv(second_rx) -> _ => {}
            recv(third_rx) -> _ => {}
            recv(fourth_rx) -> _ => {}
            default => {}
        }
    }
    black_box(warm);

    let mut samples = Vec::with_capacity(REPS);
    let mut checksum = 0i64;
    for _ in 0..REPS {
        let start = Instant::now();
        let mut total = 0i64;
        for i in 0..COUNT as i64 {
            first_tx.send(i).unwrap();
            select! {
                recv(first_rx) -> value => { if let Ok(v) = value { total = total.wrapping_add(v); } }
                recv(second_rx) -> _ => {}
                recv(third_rx) -> _ => {}
                recv(fourth_rx) -> _ => {}
                default => {}
            }
        }
        samples.push(start.elapsed().as_nanos());
        checksum = checksum.wrapping_add(total);
        black_box(checksum);
    }
    report("select", "waits", samples, COUNT);
}

fn main() {
    let which = std::env::args().nth(1).unwrap_or_else(|| "chan".to_owned());
    match which.as_str() {
        "chan" => chan(),
        "select" => choose(),
        other => panic!("unknown scenario: {other}"),
    }
}
